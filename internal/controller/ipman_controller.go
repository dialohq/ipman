package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"reflect"
	"slices"
	"strings"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/r3labs/diff/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Envs holds environment configuration values for the IPSec controller
type Envs struct {
	NamespaceName            string
	HostSocketsPath          string
	XfrminionImage           string
	XfrminionPullPolicy      string
	CharonDaemonImage        string
	CharonDaemonPullPolicy   string
	VxlandlordImage          string
	RestctlImage             string
	RestctlPullPolicy        string
	CaddyImage               string
	CaddyProxyPullPolicy     string
	IsTest                   bool
	WaitForPodTimeoutSeconds int64
	IsMonitoringEnabled      bool
	MonitoringScrapeInterval string
	MonitoringReleaseName    string
}

// IPSecConnectionReconciler reconciles IPSecConnection resources
type IPSecConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Env    Envs
}

// RequestError represents an error that occurred during a Kubernetes API request
type RequestError struct {
	ActionType string `json:"action_type"`
	Resource   string `json:"resource"`
	Err        error  `json:"error"`
}

// Error returns a formatted error string for RequestError
func (e *RequestError) Error() string {
	return fmt.Sprintf("Error while trying to %s %s: %s", e.ActionType, e.Resource, e.Err.Error())
}

// InternalError represents an error that is not the user's fault
type InternalError struct {
	Environment any    `json:"environment"`
	Location    string `json:"location"`
	Action      string `json:"action"`
	Err         error  `json:"error"`
}

// Error returns a formatted error string for RequestError
func (e *InternalError) Error() string {
	return fmt.Sprintf("Internal error occured in '%s' while doing '%s': %s. Please open an issue on github with this error message. Env: %+v", e.Location, e.Action, e.Err.Error(), e.Environment)
}

// GetClusterNodes returns a list of all node names in the cluster
func (r *IPSecConnectionReconciler) GetClusterNodes(ctx context.Context) ([]string, error) {
	nl := &corev1.NodeList{}
	err := r.List(ctx, nl)
	if err != nil {
		e := &RequestError{ActionType: "List", Resource: "Nodes", Err: err}
		return nil, e
	}
	ns := []string{}
	for _, n := range nl.Items {
		ns = append(ns, n.Name)
	}
	return ns, nil
}

// GetClusterPodsByType returns all pods in the controller's namespace with the specified pod type label
func (r *IPSecConnectionReconciler) GetClusterPodsByType(ctx context.Context, podType string) ([]corev1.Pod, error) {
	ps := &corev1.PodList{}
	vs, err := labels.ValidatedSelectorFromSet(labels.Set{
		ipmanv1.LabelPodType: podType,
	})
	if err != nil {
		e := InternalError{
			Location: "GetClusterPodsByType",
			Action:   "Creating a label selector",
			Environment: map[string]any{
				"podList": ps,
				"podType": podType,
			},
			Err: err,
		}
		return nil, &e
	}
	opts := client.ListOptions{
		LabelSelector: vs,
	}

	err = r.List(ctx, ps, opts.ApplyOptions(nil))
	if err != nil {
		e := &RequestError{ActionType: "List", Resource: "Pods", Err: err}
		return nil, e
	}

	return ps.Items, nil

}

// ExtractCharonVolumeSocketPath gets the path to the Charon socket from a pod's volume definitions
func ExtractCharonVolumeSocketPath(p *corev1.Pod) string {
	var CharonSocketVolume *corev1.Volume
	for _, c := range p.Spec.Volumes {
		if c.Name == ipmanv1.CharonSocketHostVolumeName {
			CharonSocketVolume = &c
		}
	}

	if CharonSocketVolume == nil {
		e := InternalError{
			Location: "ExtractCharonVolumeSocketPath",
			Action:   "Finding charon socket",
			Environment: map[string]any{
				"pod": *p,
			},
			Err: fmt.Errorf("CharonSocketVolume is nil"),
		}
		fmt.Println(e.Error())
	}
	return CharonSocketVolume.HostPath.Path
}

// ExtractContainerImage gets the image used by a container with the specified name in a pod
func ExtractContainerImage(p *corev1.Pod, containerName string) string {
	var img string
	for _, c := range p.Spec.Containers {
		if c.Name == containerName {
			img = c.Image
		}
	}
	return img
}

// CharonFromPod converts a Kubernetes Pod into an IpmanPod with CharonPodSpec
func CharonFromPod(p *corev1.Pod) IpmanPod[CharonPodSpec] {
	return IpmanPod[CharonPodSpec]{
		Spec: CharonPodSpec{
			HostPath: ExtractCharonVolumeSocketPath(p),
		},
		Meta: PodMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			IP:        p.Status.PodIP,
			NodeName:  p.Spec.NodeName,
			Image:     ExtractContainerImage(p, ipmanv1.CharonDaemonContainerName),
		},
	}
}

// ProxyFromPod converts a Kubernetes Pod into an IpmanPod with ProxyPodSpec
func ProxyFromPod(p *corev1.Pod) IpmanPod[ProxyPodSpec] {
	cs := ProxyPodSpec{}
	err := json.Unmarshal([]byte(p.Annotations[ipmanv1.AnnotationSpec]), &cs)
	if err != nil {
		logger := log.FromContext(context.Background())
		e := &InternalError{
			Location: "ProxyFromPod",
			Action:   "Unmarshaling spec annotation",
			Err:      err,
			Environment: map[string]any{
				"Pod": *p,
			},
		}
		logger.Error(e, "Couldn't unmarshal spec annotation of proxy pod")
	}
	return IpmanPod[ProxyPodSpec]{
		Spec: ProxyPodSpec{
			HostPath: ExtractCharonVolumeSocketPath(p),
			Configs:  cs.Configs,
		},
		Meta: PodMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			IP:        p.Status.PodIP,
			NodeName:  p.Spec.NodeName,
			Image:     ExtractContainerImage(p, ipmanv1.CharonAPIProxyContainerName),
		},
	}
}

// GetClusterPodsAs retrieves cluster pods with a specific label and transforms them into typed IpmanPod objects
func GetClusterPodsAs[S IpmanPodSpec](ctx context.Context, r *IPSecConnectionReconciler, label string, transformer func(*corev1.Pod) IpmanPod[S]) ([]IpmanPod[S], error) {
	IpmanPods := []IpmanPod[S]{}
	ps, err := r.GetClusterPodsByType(ctx, label)
	if err != nil {
		return nil, err
	}

	for _, p := range ps {
		IpmanPods = append(IpmanPods, transformer(&p))
	}
	return IpmanPods, nil
}

// XfrmFromPod converts a Kubernetes Pod into an IpmanPod with XfrmPodSpec,
// extracting properties and routes from pod annotations
func (r *IPSecConnectionReconciler) XfrmFromPod(p *corev1.Pod) IpmanPod[XfrmPodSpec] {
	specJSON := p.Annotations[ipmanv1.AnnotationSpec]

	spec := &XfrmPodSpec{}
	err := json.Unmarshal([]byte(specJSON), spec)
	if err != nil {
		fmt.Printf("Error unmarshaling XfrmPodSpec: %v\n", err)
	}
	nid, err := r.GetNodeID(p.Spec.NodeName)
	// has to exist since there is a pod there
	for err != nil {
		time.Sleep(1 * time.Second)
		fmt.Printf("Error getting node id of node '%s': %s\n", p.Spec.NodeName, err.Error())
		nid, err = r.GetNodeID(p.Spec.NodeName)
	}
	result := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			IP:        p.Status.PodIP,
			NodeName:  p.Spec.NodeName,
			NodeID:    nid,
			Image:     ExtractContainerImage(p, ipmanv1.XfrminionContainerName),
		},
		Spec: *spec,
	}

	return result
}

// FindPod finds a pod of the specified type on the given node
func FindPod[S IpmanPodSpec](ps []IpmanPod[S], node string) *IpmanPod[S] {
	for _, p := range ps {
		if p.Meta.NodeName == node {
			return &p
		}
	}
	return nil
}

// FindXfrms returns all Xfrm pods that are on the specified node
func FindXfrms(ps []IpmanPod[XfrmPodSpec], node string) []IpmanPod[XfrmPodSpec] {
	result := slices.DeleteFunc(ps, func(p IpmanPod[XfrmPodSpec]) bool {
		return p.Meta.NodeName != node
	})

	return result
}
func hasAnnotations(p *corev1.Pod) bool {
	_, ok1 := p.Annotations[ipmanv1.AnnotationChildName]
	_, ok2 := p.Annotations[ipmanv1.AnnotationIpmanName]
	_, ok3 := p.Annotations[ipmanv1.AnnotationPoolName]
	return (ok1 && ok2 && ok3)
}

func workerFromPod(p *corev1.Pod) Worker {
	return Worker{
		Meta: PodMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			IP:        p.Status.PodIP,
			NodeName:  p.Spec.NodeName,
		},
		Spec: WorkerPodSpec{
			Routes: []Route{
				Route(p.Annotations[ipmanv1.AnnotationRemoteIPs]),
			},
			OwnerConnection: p.Annotations[ipmanv1.AnnotationIpmanName],
			OwnerChild:      p.Annotations[ipmanv1.AnnotationChildName],
			VxlanIP:         p.Annotations[ipmanv1.AnnotationVxlanIP],
		},
	}
}

func (r *IPSecConnectionReconciler) GetWorkersState(ctx context.Context) (*WorkersState, error) {
	done := false
	state := &WorkersState{}
	for !done {
		pods := &corev1.PodList{}
		err := r.List(ctx, pods)
		if err != nil {
			return nil, err
		}

		state2 := WorkersState{}
		state2.Workers = []Worker{}
		for _, p := range pods.Items {
			if hasAnnotations(&p) {
				if p.Status.PodIP != "" {
					state2.Workers = append(state2.Workers, workerFromPod(&p))
				} else {
					time.Sleep(1 * time.Second)
					break
				}
			}
		}
		done = true
		state = &state2

	}
	return state, nil
}

// GetClusterState retrieves the current state of IPMan pods in the cluster
func (r *IPSecConnectionReconciler) GetClusterState(ctx context.Context) (*ClusterState, error) {
	cs := &ClusterState{
		Nodes:      []NodeState{},
		PodMonitor: true,
	}
	nodes, err := r.GetClusterNodes(ctx)
	if err != nil {
		return nil, err
	}

	pm := &promv1.PodMonitor{}
	err = r.Get(ctx, types.NamespacedName{Namespace: r.Env.NamespaceName, Name: ipmanv1.PodMonitorName}, pm)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, &RequestError{"Get", "PodMonitor", err}
		} else {
			cs.PodMonitor = false
		}
	}

	charons, err := GetClusterPodsAs(ctx, r, ipmanv1.LabelValueCharonPod, CharonFromPod)
	if err != nil {
		return nil, err
	}

	proxies, err := GetClusterPodsAs(ctx, r, ipmanv1.LabelValueProxyPod, ProxyFromPod)
	if err != nil {
		return nil, err
	}

	xfrms, err := GetClusterPodsAs(ctx, r, ipmanv1.LabelValueXfrmPod, r.XfrmFromPod)
	if err != nil {
		return nil, err
	}

	sortPods(xfrms)
	for _, n := range nodes {
		xfrmClone := make([]IpmanPod[XfrmPodSpec], len(xfrms))
		copy(xfrmClone, xfrms)

		nodeXfrms := FindXfrms(xfrmClone, n)
		ns := NodeState{
			Charon:   FindPod(charons, n),
			Proxy:    FindPod(proxies, n),
			Xfrms:    nodeXfrms,
			NodeName: n,
		}
		cs.Nodes = append(cs.Nodes, ns)
	}
	return cs, nil
}

func sortPods[Spec IpmanPodSpec](p []IpmanPod[Spec]) {
	slices.SortFunc(p, func(a, b IpmanPod[Spec]) int {
		return strings.Compare(a.Meta.Name, b.Meta.Name)
	})
}

// CreateClusterNodes creates NodeState objects for all nodes referenced in IPSecConnections
func (r *IPSecConnectionReconciler) CreateClusterNodes(cl []ipmanv1.IPSecConnection) ([]NodeState, error) {
	nodeNames := map[string]NodeInfo{}
	for _, c := range cl {
		id, err := r.GetNodeID(c.Spec.NodeName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Don't create desired state for nodes that don't exist
				continue
			} else {
				return nil, err
			}
		}
		nodeNames[c.Spec.NodeName] = NodeInfo{Name: c.Name, ID: id}
	}

	chs := r.CreateCharons(cl, nodeNames)
	prxs := r.CreateProxies(cl, nodeNames)
	xfrms := r.CreateXfrms(cl)
	sortPods(xfrms)

	ns := []NodeState{}
	for n, v := range nodeNames {
		ps := FindXfrms(slices.Clone(xfrms), n)
		ns = append(ns, NodeState{
			Charon:    FindPod(chs, n),
			Proxy:     FindPod(prxs, n),
			Xfrms:     ps,
			NodeName:  n,
			MachineID: v.ID,
		})
	}
	return ns, nil
}

func (r *IPSecConnectionReconciler) GetNodeID(name string) (string, error) {
	ns := &corev1.NodeList{}
	err := r.List(context.Background(), ns)
	if err != nil {
		return "", err
	}
	for _, n := range ns.Items {
		if n.Name == name {
			return n.Status.NodeInfo.MachineID, nil
		}
	}
	return "", nil
}

// CreateCharons creates Charon pod specifications for the given IPSecConnections
func (r *IPSecConnectionReconciler) CreateCharons(cl []ipmanv1.IPSecConnection, nodes map[string]NodeInfo) []IpmanPod[CharonPodSpec] {
	chs := []IpmanPod[CharonPodSpec]{}
	for name, info := range nodes {
		found := false
		for _, c := range cl {
			if c.Spec.NodeName == name {
				found = true
				break
			}
		}
		if found {
			ch := IpmanPod[CharonPodSpec]{}
			ch.Meta = PodMeta{
				NodeName:  name,
				NodeID:    info.ID,
				Name:      strings.Join([]string{ipmanv1.CharonPodName, info.ID}, "-"),
				Namespace: r.Env.NamespaceName,
				Image:     r.Env.CharonDaemonImage,
			}
			ch.Spec = CharonPodSpec{
				HostPath: r.Env.HostSocketsPath,
			}
			chs = append(chs, ch)
		}
	}
	return chs
}

// CreateProxies creates Proxy pod specifications for the given IPSecConnections
func (r *IPSecConnectionReconciler) CreateProxies(cl []ipmanv1.IPSecConnection, nodes map[string]NodeInfo) []IpmanPod[ProxyPodSpec] {
	prxs := []IpmanPod[ProxyPodSpec]{}
	for name, info := range nodes {
		configs := []ipmanv1.IPSecConnection{}
		for _, c := range cl {
			if c.Spec.NodeName == name {
				configs = append(configs, c)
			}
		}
		if len(configs) == 0 {
			continue
		}

		prx := IpmanPod[ProxyPodSpec]{}
		specs := []ipmanv1.IPSecConnectionSpec{}
		for _, cfg := range configs {
			specs = append(specs, cfg.Spec)
		}
		prx.Meta = PodMeta{
			NodeName:  name,
			NodeID:    info.ID,
			Name:      strings.Join([]string{ipmanv1.ProxyPodName, info.ID}, "-"),
			Namespace: r.Env.NamespaceName,
			Image:     r.Env.CaddyImage,
		}
		prx.Spec = ProxyPodSpec{
			HostPath: r.Env.HostSocketsPath,
			Configs:  specs,
		}
		prxs = append(prxs, prx)
	}
	return prxs
}

// returns time after which to requeue ipsecconnection
func (r *IPSecConnectionReconciler) updateIPSecConnectionStatus(ipsecconnection *ipmanv1.IPSecConnection, ctx context.Context) (*time.Duration, error) {
	logger := log.FromContext(ctx)

	if ipsecconnection.Status.FreeIPs == nil {
		ipsecconnection.Status.FreeIPs = map[string]map[string][]string{}
	}

	for k := range ipsecconnection.Status.FreeIPs {
		if _, ok := ipsecconnection.Spec.Children[k]; !ok {
			delete(ipsecconnection.Status.FreeIPs, k)
		}
	}

	for childName, c := range ipsecconnection.Spec.Children {
		if ipsecconnection.Status.FreeIPs[childName] == nil {
			ipsecconnection.Status.FreeIPs[childName] = map[string][]string{}
		}
		for poolName, ips := range c.IpPools {
			if ipsecconnection.Status.FreeIPs[childName][poolName] == nil {
				ipsecconnection.Status.FreeIPs[childName][poolName] = []string{}
			}

			ipsecconnection.Status.FreeIPs[childName][poolName] = slices.Clone(ips)
		}
	}

	podlist := &corev1.PodList{}
	err := r.List(ctx, podlist)
	if err != nil {
		logger.Error(err, "Error listing pods to check IPs")
		return nil, err
	}

	childNames := []string{}
	for childName := range ipsecconnection.Spec.Children {
		childNames = append(childNames, childName)
	}

	podsWithAnnotation := []*corev1.Pod{}
	for _, p := range podlist.Items {
		if slices.Contains(childNames, p.Annotations[ipmanv1.AnnotationChildName]) {
			podsWithAnnotation = append(podsWithAnnotation, &p)
		}
	}
	// TODO: if it turns out it's taking too long we can reverse sort by
	// timestamp and stop after we reach the first still valid
	maps.DeleteFunc(ipsecconnection.Status.PendingIPs, func(ip string, timestamp string) bool {
		ts, err := time.Parse(time.Layout, timestamp)
		if err != nil {
			logger.Error(err, "Malformed timestamp in pending ips", "timestamp", timestamp)
			return true
		}
		contains := slices.ContainsFunc(podsWithAnnotation, func(p *corev1.Pod) bool {
			return p.Annotations[ipmanv1.AnnotationVxlanIP] == ip
		})
		timePassed := time.Now().After(ts.Add(time.Second * time.Duration(ipmanv1.ReconcilerPendingIPsTimeoutSeconds)))

		return contains || timePassed
	})

	podIpList := []string{}
	for _, p := range podsWithAnnotation {
		podIpList = append(podIpList, p.Annotations[ipmanv1.AnnotationVxlanIP])
	}

	for cn, pools := range ipsecconnection.Status.FreeIPs {
		if ipsecconnection.Status.FreeIPs[cn] == nil {
			ipsecconnection.Status.FreeIPs[cn] = map[string][]string{}
		}
		for poolName := range pools {
			if ipsecconnection.Status.FreeIPs[cn][poolName] == nil {
				ipsecconnection.Status.FreeIPs[cn][poolName] = []string{}
			}
			pendingIps := slices.Collect(maps.Keys(ipsecconnection.Status.PendingIPs))
			ipsecconnection.Status.FreeIPs[cn][poolName] = slices.DeleteFunc(ipsecconnection.Status.FreeIPs[cn][poolName], func(ip string) bool {
				return slices.Contains(pendingIps, ip) || slices.Contains(podIpList, ip)
			})
		}
	}

	p := slices.Collect(maps.Values(ipsecconnection.Status.PendingIPs))
	sortedPending := []time.Duration{}
	for _, v := range p {
		// ignored since already checked above
		// maybe combine it if there is a lot of them :TODO
		t, _ := time.Parse(time.Layout, v)
		sortedPending = append(sortedPending, t.Add(time.Second*ipmanv1.ReconcilerPendingIPsTimeoutSeconds).Sub(time.Now()))
	}
	var requeueIn *time.Duration
	if len(sortedPending) == 0 {
		requeueIn = nil
	} else {
		min := slices.Min(sortedPending)
		requeueIn = &min
	}

	nodelist := &corev1.NodeList{}
	err = r.List(ctx, nodelist)
	if err != nil {
		logger.Error(err, "Couldn't list nodes")
		return requeueIn, err
	}
	for _, node := range nodelist.Items {
		if node.GetLabels()["kubernetes.io/hostname"] == ipsecconnection.Spec.NodeName {
			charonPod := &corev1.Pod{}
			nsn := types.NamespacedName{
				Name:      ipmanv1.ProxyPodName + "-" + node.Status.NodeInfo.MachineID,
				Namespace: r.Env.NamespaceName,
			}
			err = r.Get(ctx, nsn, charonPod)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return requeueIn, nil
				}
				return nil, fmt.Errorf("Couldn't get charon pod: %w", err)
			}
			ipsecconnection.Status.CharonProxyIP = charonPod.Status.PodIP
		}
	}
	return requeueIn, nil
}

// CreateXfrms creates Xfrm pod specifications for each child in the given IPSecConnections
func (r *IPSecConnectionReconciler) CreateXfrms(cl []ipmanv1.IPSecConnection) []IpmanPod[XfrmPodSpec] {
	ctx := context.Background()
	logger := log.FromContext(ctx)
	xfrms := []IpmanPod[XfrmPodSpec]{}
	workers, err := r.CreateWorkers(ctx)
	if err != nil {
		logger.Error(err, "Couldn't fetch workers")
		return nil
	}
	for _, conn := range cl {
		nodeid, err := r.GetNodeID(conn.Spec.NodeName)
		if err != nil {
			logger.Error(err, "Couldn't find node with specified name", "nodename", conn.Spec.NodeName)
		}
		for _, c := range conn.Spec.Children {
			ws := workers[conn.Name][c.Name]
			bfdbs := LocalRoutes{}
			for _, w := range ws {
				bfdbs[w.Spec.VxlanIP] = w.Meta.IP
			}
			x := IpmanPod[XfrmPodSpec]{}
			x.Spec = XfrmPodSpec{
				Props: XfrmProperties{
					OwnerChild:      c.Name,
					OwnerConnection: conn.Name,
					InterfaceID:     uint32(c.XfrmIfId),
					XfrmIP:          c.XfrmIP,
					VxlanIP:         c.VxlanIP,
				},
				Routes: Routes{
					Local:     c.LocalIPs,
					Remote:    c.RemoteIPs,
					BridgeFDB: bfdbs,
				},
			}
			x.Meta = PodMeta{
				Name:      strings.Join([]string{ipmanv1.XfrmPodName, c.Name, conn.Name}, "-"),
				Namespace: r.Env.NamespaceName,
				NodeName:  conn.Spec.NodeName,
				NodeID:    nodeid,
				Image:     r.Env.XfrminionImage,
			}
			xfrms = append(xfrms, x)
		}
	}
	return xfrms
}

func (r *IPSecConnectionReconciler) CreateNodes(ctx context.Context) ([]NodeState, error) {
	cl := &ipmanv1.IPSecConnectionList{}
	err := r.List(ctx, cl)
	if err != nil {
		e := &RequestError{ActionType: "List", Resource: "IPSecConnections", Err: err}
		return nil, e
	}
	ns, err := r.CreateClusterNodes(cl.Items)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(ns, func(a, b NodeState) int {
		return strings.Compare(a.NodeName, b.NodeName)
	})
	return ns, nil
}

func (r *IPSecConnectionReconciler) CreateWorkers(ctx context.Context) (map[string]map[string][]Worker, error) {
	ps := &corev1.PodList{}
	sel := labels.NewSelector()
	req, err := labels.NewRequirement(ipmanv1.LabelWorker, selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("Error creating label requirement, this is a bug in the operator: %w", err)
	}
	sel = sel.Add(*req)
	opts := client.ListOptions{}
	err = r.List(ctx, ps, &opts)
	if err != nil {
		return nil, err
	}
	// structure:
	// 	connection1:
	// 		child1:
	// 			- worker
	// 			- worker
	// 			- worker
	// 		child2:
	// 			- worker
	// 			- worker
	// 			- worker
	// 	connection2:
	// 		child3:
	// 			- worker
	// 			- worker
	// 			- worker
	// 		child4:
	// 			- worker
	// 			- worker
	// 			- worker
	//
	//
	ws := map[string]map[string][]Worker{}
	for _, p := range ps.Items {
		if p.Status.PodIP == "" {
			continue
		}
		podChn := p.Labels[ipmanv1.LabelWorker]
		podCon := p.Annotations[ipmanv1.AnnotationIpmanName]
		if _, ok := ws[podCon]; !ok {
			ws[podCon] = map[string][]Worker{}
		}
		if _, ok := ws[podCon][podChn]; !ok {
			ws[podCon][podChn] = []Worker{}
		}
		ws[podCon][podChn] = append(ws[podCon][podChn], workerFromPod(&p))
	}
	return ws, nil
}

// CreateDesiredState creates a ClusterState representing the desired state based on IPSecConnections
func (r *IPSecConnectionReconciler) CreateDesiredState(ctx context.Context) (*ClusterState, error) {
	ns, err := r.CreateNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("Error creating nodes: %w", err)
	}

	return &ClusterState{Nodes: ns, PodMonitor: r.Env.IsMonitoringEnabled}, nil
}

func IsNodeChanged(c diff.Change) bool {
	return reflect.DeepEqual(c.Path, []string{"meta", "node"})
}

func isCreated(c diff.Change) bool {
	return (len(c.Path) == 0 && c.To != nil)
}
func isDeleted(c diff.Change) bool {
	return (len(c.Path) == 0 && c.To == nil)
}

func comparePods[Spec IpmanPodSpec](desired *IpmanPod[Spec], current *IpmanPod[Spec]) bool {
	if desired == current {
		return true
	}

	if desired == nil || current == nil {
		return false
	}

	metaEqual := desired.Meta.Name == current.Meta.Name &&
		desired.Meta.Namespace == current.Meta.Namespace &&
		desired.Meta.NodeName == current.Meta.NodeName
	return metaEqual
}

func diffImmutablePod[Spec IpmanPodSpec](desired *IpmanPod[Spec], current *IpmanPod[Spec]) []Action {
	if comparePods(desired, current) {
		return []Action{}
	}

	if desired == nil {
		return []Action{&DeletePodAction[Spec]{Pod: current}}
	}

	if current == nil {
		return []Action{&CreatePodAction[Spec]{Pod: desired}}
	}

	if !comparePods(desired, current) {
		return []Action{&DeletePodAction[Spec]{Pod: current}, &CreatePodAction[Spec]{Pod: desired}}
	}

	return []Action{}
}

func (r *IPSecConnectionReconciler) diffProxy(desired *IpmanPod[ProxyPodSpec], current *IpmanPod[ProxyPodSpec], conns []ipmanv1.IPSecConnection) ([]Action, error) {
	if desired == nil && current == nil {
		return []Action{}, nil
	}
	if desired == nil {
		return []Action{&DeletePodAction[ProxyPodSpec]{Pod: current}}, nil
	}

	connsPerNode := []ipmanv1.IPSecConnection{}
	for _, c := range conns {
		if c.Spec.NodeName == desired.Meta.NodeName {
			connsPerNode = append(connsPerNode, c)
		}
	}
	if current == nil {
		return []Action{&CreatePodAction[ProxyPodSpec]{Pod: desired}, &OverrideConfigAction{PodName: desired.Meta.Name, Configs: connsPerNode}}, nil
	}

	sameMeta := comparePods(desired, current)
	if !sameMeta {
		return []Action{&DeletePodAction[ProxyPodSpec]{Pod: current}, &CreatePodAction[ProxyPodSpec]{Pod: desired}, &OverrideConfigAction{PodName: desired.Meta.Name, Configs: connsPerNode}}, nil
	}
	slices.SortFunc(desired.Spec.Configs, func(a, b ipmanv1.IPSecConnectionSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(current.Spec.Configs, func(a, b ipmanv1.IPSecConnectionSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	if !reflect.DeepEqual(desired.Spec.Configs, current.Spec.Configs) {
		return []Action{&OverrideConfigAction{PodName: current.Meta.Name, Configs: connsPerNode}}, nil
	}
	return []Action{}, nil
}

func diffCharon(desired *IpmanPod[CharonPodSpec], current *IpmanPod[CharonPodSpec]) []Action {
	return diffImmutablePod(desired, current)
}

func recreatePod[S IpmanPodSpec](old, new *IpmanPod[S]) []Action {
	return []Action{&DeletePodAction[S]{Pod: old}, &CreatePodAction[S]{Pod: new}}
}

func diffRoutes(current, desired IpmanPod[XfrmPodSpec]) ([]Action, error) {
	acts := []Action{}
	clr := current.Spec.Routes.Local
	dlr := desired.Spec.Routes.Local
	slices.Sort(clr)
	slices.Sort(dlr)
	if !reflect.DeepEqual(clr, dlr) {
		cl, _ := diff.Diff(clr, dlr)
		for _, change := range cl {
			switch change.Type {
			case "create":
				val, ok := change.To.(string)
				if !ok {
					return nil, fmt.Errorf("desired value is not a string: %+v", change)
				} else {
					acts = append(acts, &AddLocalRouteAction{Route: val, Pod: current})
				}
			case "delete":
				val, ok := change.From.(string)
				if !ok {
					return nil, fmt.Errorf("current value is not a string: %+v", change)
				} else {
					acts = append(acts, &DeleteLocalRouteAction{Route: val, Pod: current})
				}
			case "update":
				if change.To == nil {
					valFrom, ok := change.From.(string)
					if !ok {
						return nil, fmt.Errorf("On update local route, verdict=delete, from value is not a string: %+v", change)
					}
					acts = append(acts, &DeleteLocalRouteAction{Route: valFrom, Pod: current})
					break
				}
				if change.From == nil {
					valTo, ok := change.To.(string)
					if !ok {
						return nil, fmt.Errorf("On update local route, verdict=create, to value is not a string: %+v", change)
					}
					acts = append(acts, &AddLocalRouteAction{Route: valTo, Pod: current})
					break
				}
				valFrom, ok := change.From.(string)
				valTo, ok2 := change.To.(string)
				if !(ok && ok2) {
					return nil, fmt.Errorf("on update local route, verdict=update, one of the values is not a string: %+v", change)
				} else {
					acts = append(acts, &DeleteLocalRouteAction{Route: valFrom, Pod: current}, &AddLocalRouteAction{Route: valTo, Pod: current})
				}
			}
		}
	}

	crr := current.Spec.Routes.Remote
	drr := desired.Spec.Routes.Remote
	slices.Sort(crr)
	slices.Sort(drr)
	if !reflect.DeepEqual(crr, drr) {
		cl, _ := diff.Diff(crr, drr)
		for _, change := range cl {
			switch change.Type {
			case "create":
				val, ok := change.To.(string)
				if !ok {
					return nil, fmt.Errorf("desired value is not a string: %+v", change)
				} else {
					acts = append(acts, &AddRemoteRouteAction{Route: val, Pod: current})
				}
			case "delete":
				val, ok := change.From.(string)
				if !ok {
					return nil, fmt.Errorf("current value is not a string: %+v", change)
				} else {
					acts = append(acts, &DeleteRemoteRouteAction{Route: val, Pod: current})
				}
			case "update":
				if change.To == nil {
					valFrom, ok := change.From.(string)
					if !ok {
						return nil, fmt.Errorf("On update Remote route, verdict=delete, from value is not a string: %+v", change)
					}
					acts = append(acts, &DeleteRemoteRouteAction{Route: valFrom, Pod: current})
					break
				}
				if change.From == nil {
					valTo, ok := change.To.(string)
					if !ok {
						return nil, fmt.Errorf("On update Remote route, verdict=create, to value is not a string: %+v", change)
					}
					acts = append(acts, &AddRemoteRouteAction{Route: valTo, Pod: current})
					break
				}
				valFrom, ok := change.From.(string)
				valTo, ok2 := change.To.(string)
				if !(ok && ok2) {
					return nil, fmt.Errorf("on update Remote route, verdict=update, one of the values is not a string: %+v", change)
				} else {
					acts = append(acts, &DeleteRemoteRouteAction{Route: valFrom, Pod: current}, &AddRemoteRouteAction{Route: valTo, Pod: current})
				}
			}
		}
	}

	cfdb := current.Spec.Routes.BridgeFDB
	dfdb := desired.Spec.Routes.BridgeFDB
	if !reflect.DeepEqual(cfdb, dfdb) {
		cl, _ := diff.Diff(cfdb, dfdb)
		for _, c := range cl {
			switch c.Type {
			case "update":
				if c.To == nil {
					valFrom, ok := c.From.(string)
					if !ok {
						return nil, fmt.Errorf("On update bridge fdb, verdict=delete, from value is not a string: %+v", c)
					}
					acts = append(acts, &DeleteBridgeFDBAction{UnderlyingIP: valFrom, Pod: current, VxlanIP: c.Path[0]})
					break
				}
				if c.From == nil {
					valTo, ok := c.To.(string)
					if !ok {
						return nil, fmt.Errorf("On update bridge fdb, verdict=create, to value is not a string: %+v", c)
					}
					acts = append(acts, &AddBridgeFDBAction{UnderlyingIP: valTo, Pod: current, VxlanIP: c.Path[0]})
					break
				}
				valFrom, ok := c.From.(string)
				valTo, ok2 := c.To.(string)
				if !(ok && ok2) {
					return nil, fmt.Errorf("on update bridge fdb, verdict=update, one of the values is not a string: %+v", c)
				} else {
					acts = append(acts, &DeleteBridgeFDBAction{UnderlyingIP: valFrom, Pod: current, VxlanIP: c.Path[0]}, &AddBridgeFDBAction{UnderlyingIP: valTo, Pod: current, VxlanIP: c.Path[0]})
				}
			case "create":
				val, ok := c.To.(string)
				if !ok {
					return nil, fmt.Errorf("desired value is not a string: %+v", c)
				} else {
					acts = append(acts, &AddBridgeFDBAction{UnderlyingIP: val, VxlanIP: c.Path[0], Pod: current})
				}
			case "delete":
				val, ok := c.From.(string)
				if !ok {
					return nil, fmt.Errorf("desired value is not a string: %+v", c)
				} else {
					acts = append(acts, &DeleteBridgeFDBAction{UnderlyingIP: val, VxlanIP: c.Path[0], Pod: current})
				}
			default:
				return nil, fmt.Errorf("Unexpected operation, expected 'update', 'create' or 'delete'. Got: %+v", c)
			}
		}
	}
	return acts, nil
}

// createXfrmPod returns a list of actions to get a xfrm pod with desired routes
func createXfrmPod(x IpmanPod[XfrmPodSpec]) []Action {
	acts := []Action{}
	rs := x.Spec.Routes
	x.Spec.Routes = Routes{}
	acts = append(acts, &CreatePodAction[XfrmPodSpec]{Pod: &x})

	for _, lr := range rs.Local {
		acts = append(acts, &AddLocalRouteAction{Route: lr, Pod: x})
	}
	for _, rr := range rs.Remote {
		acts = append(acts, &AddRemoteRouteAction{Route: rr, Pod: x})
	}
	for vxlanIP, podIP := range rs.BridgeFDB {
		acts = append(acts, &AddBridgeFDBAction{VxlanIP: vxlanIP, UnderlyingIP: podIP, Pod: x})
	}
	return acts
}

func compareXfrmPods(a, b IpmanPod[XfrmPodSpec]) bool {
	if &a == &b {
		return true
	}

	metaEqual := a.Meta.Name == b.Meta.Name &&
		a.Meta.Namespace == b.Meta.Namespace &&
		a.Meta.NodeName == b.Meta.NodeName

	propsEqual := a.Spec.Props.OwnerChild == b.Spec.Props.OwnerChild &&
		a.Spec.Props.OwnerConnection == b.Spec.Props.OwnerConnection &&
		a.Spec.Props.InterfaceID == b.Spec.Props.InterfaceID &&
		a.Spec.Props.XfrmIP == b.Spec.Props.XfrmIP &&
		a.Spec.Props.VxlanIP == b.Spec.Props.VxlanIP

	routesEqual := reflect.DeepEqual(a.Spec.Routes.Local, b.Spec.Routes.Local) &&
		reflect.DeepEqual(a.Spec.Routes.Remote, b.Spec.Routes.Remote) &&
		reflect.DeepEqual(a.Spec.Routes.BridgeFDB, b.Spec.Routes.BridgeFDB)

	return metaEqual && propsEqual && routesEqual
}

func diffXfrms(desired, current []IpmanPod[XfrmPodSpec]) []Action {
	logger := log.FromContext(context.Background())

	compareXfrmPodsLists := func(desired, current []IpmanPod[XfrmPodSpec]) bool {
		if len(desired) != len(current) {
			return false
		}

		slices.SortFunc(desired, func(a, b IpmanPod[XfrmPodSpec]) int {
			return strings.Compare(a.Meta.Name, b.Meta.Name)
		})
		slices.SortFunc(current, func(a, b IpmanPod[XfrmPodSpec]) int {
			return strings.Compare(a.Meta.Name, b.Meta.Name)
		})
		for i := range desired {
			if !compareXfrmPods(desired[i], current[i]) {
				return false
			}
		}
		return true
	}

	if compareXfrmPodsLists(desired, current) {
		return []Action{}
	}
	// This should be sufficient since namespace will always be the same,
	// and node has to be checked anyway, and name contains owner connection
	// and owner child info
	compareXfrms := func(a, b IpmanPod[XfrmPodSpec]) int {
		return strings.Compare(a.Meta.Name, b.Meta.Name)
	}

	acts := []Action{}
	for _, x := range desired {
		exists := false
		idx := -1
		for i, p := range current {
			if p.Meta.Name == x.Meta.Name {
				exists = true
				idx = i
				break
			}
		}
		if !exists {
			created := createXfrmPod(x)
			acts = append(acts, created...)
			continue
		}

		if compareXfrmPods(x, current[idx]) {
			continue
		}

		if x.Spec.Props.OwnerChild != current[idx].Spec.Props.OwnerChild ||
			x.Spec.Props.OwnerConnection != current[idx].Spec.Props.OwnerConnection ||
			x.Spec.Props.InterfaceID != current[idx].Spec.Props.InterfaceID ||
			x.Spec.Props.XfrmIP != current[idx].Spec.Props.XfrmIP ||
			x.Spec.Props.VxlanIP != current[idx].Spec.Props.VxlanIP {
			acts = append(acts, recreatePod(&current[idx], &x)...)
			continue
		}

		if x.Meta.NodeName != current[idx].Meta.NodeName {
			acts = append(acts, recreatePod(&current[idx], &x)...)
			continue
		}

		if !reflect.DeepEqual(x.Spec.Routes, current[idx].Spec.Routes) {
			actions, err := diffRoutes(current[idx], x)
			if err != nil {
				logger.Error(err, "Route in xfrm is invalid")
			} else {
				acts = append(acts, actions...)
			}
		}
	}

	for _, x := range current {
		_, exists := slices.BinarySearchFunc(desired, x, compareXfrms)
		if !exists {
			acts = append(acts, &DeletePodAction[XfrmPodSpec]{Pod: &x})
		}
	}

	return acts
}

func findNode(name string, s *ClusterState) (int, bool) {
	for i, n := range s.Nodes {
		if n.NodeName == name {
			return i, true
		}
	}
	return -1, false
}

func deleteNode(ns *NodeState) []Action {
	acts := []Action{}
	if ns.Charon != nil {
		acts = append(acts, &DeletePodAction[CharonPodSpec]{Pod: ns.Charon})
	}
	if ns.Proxy != nil {
		acts = append(acts, &DeletePodAction[ProxyPodSpec]{Pod: ns.Proxy})
	}
	for _, x := range ns.Xfrms {
		acts = append(acts, &DeletePodAction[XfrmPodSpec]{Pod: &x})
	}

	return acts
}

// DiffStates compares desired and current cluster states and returns actions needed to reconcile them
func (r *IPSecConnectionReconciler) DiffStates(desired *ClusterState, current *ClusterState, conns []ipmanv1.IPSecConnection) ([]Action, error) {
	acts := []Action{}
	if desired.PodMonitor == true && current.PodMonitor == false {
		acts = append(acts, &CreateMonitorAction{})
	}

	if desired.PodMonitor == false && current.PodMonitor == true {
		acts = append(acts, &DeleteMonitorAction{})
	}

	for _, ns := range desired.Nodes {
		idx, found := findNode(ns.NodeName, current)
		if !found {
			fmt.Printf("Couldn't find node in current cluster state %s, skipping...\n", ns.NodeName)
			continue
		}

		if !reflect.DeepEqual(ns, current.Nodes[idx]) {
			if !reflect.DeepEqual(ns.Charon, current.Nodes[idx].Charon) {
				charonActions := diffCharon(ns.Charon, current.Nodes[idx].Charon)
				acts = append(acts, charonActions...)
			}

			if !reflect.DeepEqual(ns.Proxy, current.Nodes[idx].Proxy) {
				proxyActions, err := r.diffProxy(ns.Proxy, current.Nodes[idx].Proxy, conns)
				if err != nil {
					return nil, err
				}
				acts = append(acts, proxyActions...)
			}

			if !reflect.DeepEqual(ns.Xfrms, current.Nodes[idx].Xfrms) {
				xfrmActions := diffXfrms(ns.Xfrms, current.Nodes[idx].Xfrms)
				acts = append(acts, xfrmActions...)
			}
		}
	}

	for _, cns := range current.Nodes {
		_, found := findNode(cns.NodeName, desired)
		if !found {
			acts = append(acts, deleteNode(&cns)...)
		}
	}

	return acts, nil
}

func res(rq *time.Duration, times ...time.Duration) ctrl.Result {
	if rq == nil && times == nil {
		return ctrl.Result{}
	}
	if rq == nil {
		return ctrl.Result{RequeueAfter: slices.Min(times)}
	}

	times = append(times, *rq)
	min := slices.Min(times)

	return ctrl.Result{RequeueAfter: min}
}

func (r *IPSecConnectionReconciler) UpdateStatus(ctx context.Context) (*time.Duration, error) {
	logger := log.FromContext(ctx)
	list := ipmanv1.IPSecConnectionList{}
	var rq *time.Duration
	err := r.List(ctx, &list)
	if err != nil {
		logger.Error(err, "Couldn't fetch ipsec connection list to update status")
		return nil, err
	}
	for _, ipsc := range list.Items {
		rq2, err := r.updateIPSecConnectionStatus(&ipsc, ctx)
		rq = rq2
		if err != nil {
			logger.Error(err, "Couldn't prepare update to ipsec connection status", "connection", ipsc.Name)
			return rq, err
		}
		err = r.Status().Update(ctx, &ipsc)
		if err != nil {
			fmt.Println("Couldn't update status", err)
			return rq, err
		}
	}
	return rq, nil
}

// Reconcile implements the reconciliation loop for IPSecConnection resources
func (r *IPSecConnectionReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting reconciler loop")
	pod := corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, &pod)
	if !apierrors.IsNotFound(err) {
		if err != nil {
			logger.Error(err, "Error fetching pod")
			return ctrl.Result{RequeueAfter: time.Duration(5 * time.Second)}, err
		} else {
			if pod.Status.PodIP == "" {
				logger.Info("Pod doesn't have ip yet, requeuing...")
				return ctrl.Result{RequeueAfter: time.Duration(1 * time.Second)}, nil
			}
		}
	}
	rq, err := r.UpdateStatus(ctx)
	ctr := 1
	for apierrors.IsConflict(err) && ctr <= ipmanv1.UpdateStatusMaxRetries {
		logger.Info("Error updating status, trying again", "tries", fmt.Sprintf("%d/%d", ctr, ipmanv1.UpdateStatusMaxRetries), "error", err)
		rq, err = r.UpdateStatus(ctx)
		ctr += 1
	}

	if err != nil {
		if ctr == ipmanv1.UpdateStatusMaxRetries+1 {
			dur := time.Duration(5 * time.Second)
			return res(&dur), fmt.Errorf("Error updating status after %d tries: %w", ipmanv1.UpdateStatusMaxRetries, err)
		}
	}
	currentState, err := r.GetClusterState(ctx)
	if err != nil {
		if errors.Is(err, &RequestError{}) {
			return res(rq, time.Duration(time.Second*3)), err
		}
		logger.Error(err, "Error getting cluster state")
		return res(rq, time.Duration(time.Second*3)), err
	}

	desiredState, err := r.CreateDesiredState(ctx)
	if err != nil {
		if errors.Is(err, &RequestError{}) {
			return res(rq, time.Duration(time.Second*3)), err
		}
		logger.Error(err, "Error creating desired cluster state")
		return res(rq, time.Duration(time.Second*3)), err
	}

	cl := &ipmanv1.IPSecConnectionList{}
	err = r.List(ctx, cl)
	if err != nil {
		return res(rq), fmt.Errorf("Couldn't list ipmen: %w", err)
	}

	actions, err := r.DiffStates(desiredState, currentState, cl.Items)
	if err != nil {
		return res(rq), fmt.Errorf("Error diffing states: %w", err)
	}
	actionTypes := []string{}
	for _, a := range actions {
		actionTypes = append(actionTypes, reflect.TypeOf(a).String())
	}
	for _, a := range actions {
		logger.Info("Doing action", "type", reflect.TypeOf(a))
		err = a.Do(ctx, r)
		if err != nil {
			logger.Info("Error executing action", "action", a, "msg", err)
			return res(rq, time.Duration(5*time.Second)), err
		}
	}

	return res(rq), nil
}

// SetupWithManager sets up the controller with the Manager
func (r *IPSecConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	podPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.GetAnnotations()[ipmanv1.AnnotationChildName]
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := e.Object.GetAnnotations()[ipmanv1.AnnotationChildName]
			return ok
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&ipmanv1.IPSecConnection{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(podPredicate)).
		Complete(r)
}
