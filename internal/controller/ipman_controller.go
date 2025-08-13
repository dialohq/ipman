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
	u "dialo.ai/ipman/pkg/utils"
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
	XfrminjectorImage        string
	XfrminjectorPullPolicy   string
	XfrminjectorTTL          *int32
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
			HostPath:    ExtractCharonVolumeSocketPath(p),
			HostNetwork: p.Spec.HostNetwork,
		},
		Annotations: p.Annotations,
		Group: ipmanv1.CharonGroupRef{
			Name:      p.Labels[ipmanv1.LabelGroupName],
			Namespace: p.Labels[ipmanv1.LabelGroupNamespace],
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
func ProxyFromPod(p *corev1.Pod) IpmanPod[RestctlPodSpec] {
	cs := RestctlPodSpec{}
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
	return IpmanPod[RestctlPodSpec]{
		Spec: RestctlPodSpec{
			HostPath: ExtractCharonVolumeSocketPath(p),
			Configs:  cs.Configs,
		},
		Group: ipmanv1.CharonGroupRef{
			Name:      p.Labels[ipmanv1.LabelGroupName],
			Namespace: p.Labels[ipmanv1.LabelGroupNamespace],
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
	result := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			IP:        p.Status.PodIP,
			NodeName:  p.Spec.NodeName,
			Image:     ExtractContainerImage(p, ipmanv1.XfrminionContainerName),
		},
		Group: ipmanv1.CharonGroupRef{
			Name:      p.Labels[ipmanv1.LabelGroupName],
			Namespace: p.Labels[ipmanv1.LabelGroupNamespace],
		},
		Spec: *spec,
	}

	return result
}

// FindPod finds a pod of the specified type on the given node
func FindPod[S IpmanPodSpec](ps []IpmanPod[S], gr ipmanv1.CharonGroupRef) *IpmanPod[S] {
	for _, p := range ps {
		if p.Group.Name == gr.Name && p.Group.Namespace == p.Group.Namespace {
			return &p
		}
	}
	return nil
}

// FindXfrms returns all Xfrm pods that are on the specified node
func FindXfrms(ps []IpmanPod[XfrmPodSpec], gr ipmanv1.CharonGroupRef) []IpmanPod[XfrmPodSpec] {
	result := slices.DeleteFunc(ps, func(p IpmanPod[XfrmPodSpec]) bool {
		return (p.Group.Name != gr.Name || p.Group.Namespace != gr.Namespace)
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
		Groups:     []GroupState{},
		PodMonitor: true,
	}
	groups := ipmanv1.CharonGroupList{}
	err := r.List(ctx, &groups)
	if err != nil {
		return nil, &RequestError{"List", "CharonGroups", err}
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

	proxies, err := GetClusterPodsAs(ctx, r, ipmanv1.LabelValueRestctlPod, ProxyFromPod)
	if err != nil {
		return nil, err
	}

	xfrms, err := GetClusterPodsAs(ctx, r, ipmanv1.LabelValueXfrmPod, r.XfrmFromPod)
	if err != nil {
		return nil, err
	}

	sortPods(xfrms)
	for _, g := range groups.Items {
		ref := ipmanv1.CharonGroupRef{Name: g.Name, Namespace: g.Namespace}
		xfrmClone := make([]IpmanPod[XfrmPodSpec], len(xfrms))
		copy(xfrmClone, xfrms)

		nodeXfrms := FindXfrms(xfrmClone, ref)
		ns := GroupState{
			Charon:   FindPod(charons, ref),
			Proxy:    FindPod(proxies, ref),
			Xfrms:    nodeXfrms,
			GroupRef: ref,
		}
		cs.Groups = append(cs.Groups, ns)
	}
	return cs, nil
}

func sortPods[Spec IpmanPodSpec](p []IpmanPod[Spec]) {
	slices.SortFunc(p, func(a, b IpmanPod[Spec]) int {
		return strings.Compare(a.Meta.Name, b.Meta.Name)
	})
}

// CreateClusterNodes creates NodeState objects for all nodes referenced in IPSecConnections
func (r *IPSecConnectionReconciler) CreateClusterNodes(cl []ipmanv1.IPSecConnection) ([]GroupState, error) {
	groups := map[types.NamespacedName]ipmanv1.CharonGroup{}
	for _, c := range cl {
		group, err := r.GetGroup(c.Spec.Group)
		if err != nil {
			return nil, err
		}
		groups[group.Nsn()] = group
	}

	chs := r.CreateCharons(groups)
	prxs := r.CreateRestctls(cl, groups)
	xfrms := r.CreateXfrms(cl)
	sortPods(xfrms)

	gs := []GroupState{}
	for nsn := range groups {
		ref := ipmanv1.CharonGroupRef{Name: nsn.Name, Namespace: nsn.Namespace}
		ps := FindXfrms(slices.Clone(xfrms), ref)
		charon := FindPod(chs, ref)
		proxy := FindPod(prxs, ref)
		g := GroupState{
			Charon:   charon,
			Proxy:    proxy,
			Xfrms:    ps,
			GroupRef: ref,
		}
		gs = append(gs, g)
	}
	return gs, nil
}

func (r *IPSecConnectionReconciler) GetGroup(gr ipmanv1.CharonGroupRef) (ipmanv1.CharonGroup, error) {
	gs := &ipmanv1.CharonGroupList{}
	err := r.List(context.Background(), gs)
	if err != nil {
		return ipmanv1.CharonGroup{}, err
	}
	for _, g := range gs.Items {
		if g.Name == gr.Name {
			return g, nil
		}
	}
	return ipmanv1.CharonGroup{}, fmt.Errorf("Charon group %s not found", gr.Name)
}

// CreateCharons creates Charon pod specifications for the given IPSecConnections
func (r *IPSecConnectionReconciler) CreateCharons(groups map[types.NamespacedName]ipmanv1.CharonGroup) []IpmanPod[CharonPodSpec] {
	chs := []IpmanPod[CharonPodSpec]{}

	for _, g := range groups {
		path := strings.Join([]string{g.Name, g.Namespace}, "/")
		fullPath := r.Env.HostSocketsPath
		if strings.HasSuffix(fullPath, "/") {
			fullPath += path
		} else {
			fullPath += "/" + path
		}

		ch := IpmanPod[CharonPodSpec]{
			Meta: PodMeta{
				Name:      strings.Join([]string{ipmanv1.CharonPodName, g.Namespace, g.Name}, "-"),
				NodeName:  g.Spec.NodeName,
				Namespace: r.Env.NamespaceName,
				Image:     r.Env.CharonDaemonImage,
			},
			Group: ipmanv1.CharonGroupRef{Name: g.Name, Namespace: g.Namespace},
			Spec: CharonPodSpec{
				HostPath:    fullPath,
				HostNetwork: g.Spec.HostNetwork,
			},
			Annotations: g.Spec.CharonExtraAnnotations,
		}
		chs = append(chs, ch)
	}
	return chs
}

// CreateRestctls creates Proxy pod specifications for the given IPSecConnections
func (r *IPSecConnectionReconciler) CreateRestctls(cl []ipmanv1.IPSecConnection, groups map[types.NamespacedName]ipmanv1.CharonGroup) []IpmanPod[RestctlPodSpec] {
	rctls := []IpmanPod[RestctlPodSpec]{}
	for nsn, group := range groups {
		path := strings.Join([]string{group.Name, group.Namespace}, "/")
		fullPath := r.Env.HostSocketsPath
		if strings.HasSuffix(fullPath, "/") {
			fullPath += path
		} else {
			fullPath += "/" + path
		}

		configs := []ipmanv1.IPSecConnection{}
		for _, c := range cl {
			gnsn := c.Spec.Group.Nsn()
			if gnsn.Name == nsn.Name && gnsn.Namespace == nsn.Namespace {
				configs = append(configs, c)
			}
		}

		if len(configs) == 0 {
			continue
		}
		rctl := IpmanPod[RestctlPodSpec]{}

		specs := u.MapF(func(v ipmanv1.IPSecConnection) ipmanv1.IPSecConnectionSpec {
			return v.Spec
		}, configs)

		rctl.Meta = PodMeta{
			NodeName:  group.Spec.NodeName,
			Name:      strings.Join([]string{ipmanv1.RestctlPodName, nsn.Namespace, nsn.Name}, "-"),
			Namespace: r.Env.NamespaceName,
			Image:     r.Env.CaddyImage,
		}
		rctl.Group = ipmanv1.CharonGroupRef{Name: nsn.Name, Namespace: nsn.Namespace}
		rctl.Spec = RestctlPodSpec{
			HostPath: fullPath,
			Configs:  specs,
		}
		rctls = append(rctls, rctl)
	}
	return rctls
}

// returns time after which to requeue ipsecconnection
func (r *IPSecConnectionReconciler) updateIPSecConnectionStatus(conn *ipmanv1.IPSecConnection, ctx context.Context) (*time.Duration, error) {
	logger := log.FromContext(ctx)

	if conn.Status.FreeIPs == nil {
		conn.Status.FreeIPs = map[string]map[string][]string{}
	}

	for k := range conn.Status.FreeIPs {
		if _, ok := conn.Spec.Children[k]; !ok {
			delete(conn.Status.FreeIPs, k)
		}
	}

	for childName, c := range conn.Spec.Children {
		if conn.Status.FreeIPs[childName] == nil {
			conn.Status.FreeIPs[childName] = map[string][]string{}
		}
		for poolName, ips := range c.IpPools {
			if conn.Status.FreeIPs[childName][poolName] == nil {
				conn.Status.FreeIPs[childName][poolName] = []string{}
			}

			conn.Status.FreeIPs[childName][poolName] = slices.Clone(ips)
		}
	}

	podlist := &corev1.PodList{}
	err := r.List(ctx, podlist)
	if err != nil {
		logger.Error(err, "Error listing pods to check IPs")
		return nil, err
	}

	childNames := []string{}
	for childName := range conn.Spec.Children {
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
	maps.DeleteFunc(conn.Status.PendingIPs, func(ip string, timestamp string) bool {
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

	for cn, pools := range conn.Status.FreeIPs {
		if conn.Status.FreeIPs[cn] == nil {
			conn.Status.FreeIPs[cn] = map[string][]string{}
		}
		for poolName := range pools {
			if conn.Status.FreeIPs[cn][poolName] == nil {
				conn.Status.FreeIPs[cn][poolName] = []string{}
			}
			pendingIps := slices.Collect(maps.Keys(conn.Status.PendingIPs))
			conn.Status.FreeIPs[cn][poolName] = slices.DeleteFunc(conn.Status.FreeIPs[cn][poolName], func(ip string) bool {
				return slices.Contains(pendingIps, ip) || slices.Contains(podIpList, ip)
			})
		}
	}

	p := slices.Collect(maps.Values(conn.Status.PendingIPs))
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
		group, err := r.GetGroup(conn.Spec.Group)
		if err != nil {
			return requeueIn, err
		}
		if node.GetLabels()["kubernetes.io/hostname"] == group.Spec.NodeName {
			charonPod := &corev1.Pod{}
			nsn := types.NamespacedName{
				Name:      ipmanv1.RestctlPodName + "-" + node.Status.NodeInfo.MachineID,
				Namespace: r.Env.NamespaceName,
			}
			err = r.Get(ctx, nsn, charonPod)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return requeueIn, nil
				}
				return nil, fmt.Errorf("Couldn't get charon pod: %w", err)
			}
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
		group, err := r.GetGroup(conn.Spec.Group)
		if err != nil {
			logger.Error(err, "Couldn't find group by reference", "group", conn.Spec.Group)
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
			x.Group = conn.Spec.Group
			x.Meta = PodMeta{
				Name:      strings.Join([]string{ipmanv1.XfrmPodName, c.Name, conn.Name}, "-"),
				Namespace: r.Env.NamespaceName,
				NodeName:  group.Spec.NodeName,
				Image:     r.Env.XfrminionImage,
			}
			xfrms = append(xfrms, x)
		}
	}
	return xfrms
}

func (r *IPSecConnectionReconciler) CreateNodes(ctx context.Context) ([]GroupState, error) {
	cl := &ipmanv1.IPSecConnectionList{}
	err := r.List(ctx, cl)
	if err != nil {
		e := &RequestError{ActionType: "List", Resource: "IPSecConnections", Err: err}
		return nil, e
	}
	gs, err := r.CreateClusterNodes(cl.Items)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(gs, func(a, b GroupState) int {
		return strings.Compare(a.GroupRef.Name, b.GroupRef.Name)
	})
	return gs, nil
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
	gs, err := r.CreateNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("Error creating groups: %w", err)
	}

	return &ClusterState{Groups: gs, PodMonitor: r.Env.IsMonitoringEnabled}, nil
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

func (r *IPSecConnectionReconciler) diffProxy(desired *IpmanPod[RestctlPodSpec], current *IpmanPod[RestctlPodSpec], conns []ipmanv1.IPSecConnection) ([]Action, error) {
	if desired == nil && current == nil {
		return []Action{}, nil
	}
	if desired == nil {
		return []Action{&DeletePodAction[RestctlPodSpec]{Pod: current}}, nil
	}

	connsPerGroup := []ipmanv1.IPSecConnection{}
	for _, c := range conns {
		if c.Spec.Group.Name == desired.Group.Name && c.Spec.Group.Namespace == desired.Group.Namespace {
			connsPerGroup = append(connsPerGroup, c)
		}
	}
	if current == nil {
		return []Action{&CreatePodAction[RestctlPodSpec]{Pod: desired}, &OverrideConfigAction{PodName: desired.Meta.Name, Configs: connsPerGroup}}, nil
	}

	sameMeta := comparePods(desired, current)
	if !sameMeta {
		return []Action{&DeletePodAction[RestctlPodSpec]{Pod: current}, &CreatePodAction[RestctlPodSpec]{Pod: desired}, &OverrideConfigAction{PodName: desired.Meta.Name, Configs: connsPerGroup}}, nil
	}
	slices.SortFunc(desired.Spec.Configs, func(a, b ipmanv1.IPSecConnectionSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(current.Spec.Configs, func(a, b ipmanv1.IPSecConnectionSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	if !reflect.DeepEqual(desired.Spec.Configs, current.Spec.Configs) {
		return []Action{&OverrideConfigAction{PodName: current.Meta.Name, Configs: connsPerGroup}}, nil
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

func findGroup(ref ipmanv1.CharonGroupRef, s *ClusterState) (int, bool) {
	for i, g := range s.Groups {
		if g.GroupRef.Name == ref.Name && g.GroupRef.Namespace == ref.Namespace {
			return i, true
		}
	}
	return -1, false
}

func deleteNode(ns *GroupState) []Action {
	acts := []Action{}
	if ns.Charon != nil {
		acts = append(acts, &DeletePodAction[CharonPodSpec]{Pod: ns.Charon})
	}
	if ns.Proxy != nil {
		acts = append(acts, &DeletePodAction[RestctlPodSpec]{Pod: ns.Proxy})
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

	for _, gs := range desired.Groups {
		idx, found := findGroup(gs.GroupRef, current)
		if !found {
			fmt.Printf("Couldn't find group %s in current cluster state, skipping...\n", gs.GroupRef.Name)
			continue
		}

		if !reflect.DeepEqual(gs, current.Groups[idx]) {
			if !reflect.DeepEqual(gs.Charon, current.Groups[idx].Charon) {
				charonActions := diffCharon(gs.Charon, current.Groups[idx].Charon)
				acts = append(acts, charonActions...)
			}

			if !reflect.DeepEqual(gs.Proxy, current.Groups[idx].Proxy) {
				proxyActions, err := r.diffProxy(gs.Proxy, current.Groups[idx].Proxy, conns)
				if err != nil {
					return nil, err
				}
				acts = append(acts, proxyActions...)
			}

			if !reflect.DeepEqual(gs.Xfrms, current.Groups[idx].Xfrms) {
				xfrmActions := diffXfrms(gs.Xfrms, current.Groups[idx].Xfrms)
				acts = append(acts, xfrmActions...)
			}
		}
	}

	for _, cgs := range current.Groups {
		_, found := findGroup(cgs.GroupRef, desired)
		if !found {
			acts = append(acts, deleteNode(&cgs)...)
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
			logger.Error(err, "Couldn't update status")
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
			return ctrl.Result{}, err
		} else {
			if pod.Status.PodIP == "" {
				logger.Info("Pod doesn't have ip yet, requeuing...")
				return ctrl.Result{RequeueAfter: time.Second}, nil
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
			return ctrl.Result{}, fmt.Errorf("Error updating status after %d tries: %w", ipmanv1.UpdateStatusMaxRetries, err)
		}
	}
	currentState, err := r.GetClusterState(ctx)
	if err != nil {
		if errors.Is(err, &RequestError{}) {
			return ctrl.Result{}, err
		}
		logger.Error(err, "Error getting cluster state")
		return ctrl.Result{}, err
	}

	desiredState, err := r.CreateDesiredState(ctx)
	if err != nil {
		if errors.Is(err, &RequestError{}) {
			return ctrl.Result{}, err
		}
		logger.Error(err, "Error creating desired cluster state")
		return ctrl.Result{}, err
	}

	cl := &ipmanv1.IPSecConnectionList{}
	err = r.List(ctx, cl)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("Couldn't list ipmen: %w", err)
	}

	actions, err := r.DiffStates(desiredState, currentState, cl.Items)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("Error diffing states: %w", err)
	}
	actionTypes := []string{}
	for _, a := range actions {
		// TODO: possibly have the action type .string()
		// to print the most important info instead of
		// relying on reflection
		str := reflect.TypeOf(a).String()
		actionTypes = append(actionTypes, str)
	}
	if len(actions) == 0 {
		logger.Info("No actions are needed")
	} else {
		logger.Info("Prepared actions", "actions", actionTypes)
	}
	for _, a := range actions {
		logger.Info("Doing action", "type", reflect.TypeOf(a))
		err = a.Do(ctx, r)
		if err != nil {
			logger.Info("Error executing action", "action", a, "msg", err)
			return ctrl.Result{}, err
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
