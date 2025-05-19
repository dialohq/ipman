package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
)

// Helper to get string env vars with fallback
defaultEnv := func(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Helper to get int env vars with fallback
getEnvInt := func(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

var (
	CharonPodName             = defaultEnv("CHARON_POD_NAME", "charon-pod")
	CharonApiSocketVolumePath = defaultEnv("CHARON_API_SOCKET_VOLUME_PATH", "/restctlsock/")
	CharonApiSocketVolumeName = defaultEnv("CHARON_API_SOCKET_VOLUME_NAME", "restctl")
	CharonSocketVolume        = defaultEnv("CHARON_SOCKET_VOLUME", "charon-volume")
	CharonConfVolume          = defaultEnv("CHARON_CONF_VOLUME", "charon-conf")
	CharonContainerImage      = defaultEnv("CHARON_CONTAINER_IMAGE", "plan9better/strongswan-charon:0.0.1")
	XfrmPodName               = defaultEnv("XFRM_POD_NAME", "xfrm-pod")

	IpmanNamespace            = defaultEnv("IPMAN_NAMESPACE", "ims")
	VxlanIpAnnotationKey      = defaultEnv("VXLAN_IP_ANNOTATION_KEY", "ipman.dialo.ai/vxlanIp")
	ChildNameAnnotationKey    = defaultEnv("CHILD_NAME_ANNOTATION_KEY", "ipman.dialo.ai/childName")
	IpmanNameAnnotationKey    = defaultEnv("IPMAN_NAME_ANNOTATION_KEY", "ipman.dialo.ai/ipmanName")
	InterfaceIdAnnotationKey  = defaultEnv("INTERFACE_ID_ANNOTATION_KEY", "ipman.dialo.ai/interfaceId")
	PendingIpTimeoutSeconds   = getEnvInt("PENDING_IP_TIMEOUT_SECONDS", 35)
)

type IpmanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *IpmanReconciler) isReconcilingKindIpman(ctx context.Context, req reconcile.Request) (bool, error) {
	im := &ipmanv1.Ipman{}
	err := r.Get(ctx, req.NamespacedName, im)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *IpmanReconciler) isReconcilingKindPod(ctx context.Context, req reconcile.Request) (bool, error) {
	pod := &corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	if pod.GetDeletionTimestamp() != nil {
		return false, nil
	}
	return true, nil
}

// returns time after which to requeue ipman
func (r *IpmanReconciler) updateIpmanStatus(ipman *ipmanv1.Ipman, ctx context.Context) (*time.Duration, error) {
	logger := log.FromContext(ctx)

	if ipman.Status.FreeIPs == nil {
		ipman.Status.FreeIPs = map[string]map[string][]string{}
	}

	for k := range ipman.Status.FreeIPs {
		if _, ok := ipman.Spec.Children[k]; !ok {
			delete(ipman.Status.FreeIPs, k)
		}
	}

	for childName, c := range ipman.Spec.Children {
		if ipman.Status.FreeIPs[childName] == nil {
			ipman.Status.FreeIPs[childName] = map[string][]string{}
		}
		for poolName, ips := range c.IpPools {
			if ipman.Status.FreeIPs[childName][poolName] == nil {
				ipman.Status.FreeIPs[childName][poolName] = []string{}
			}

			ipman.Status.FreeIPs[childName][poolName] = slices.Clone(ips)
		}
	}

	podlist := &corev1.PodList{}
	err := r.List(ctx, podlist)
	if err != nil {
		logger.Error(err, "Error listing pods to check IPs")
		return nil, err
	}

	childNames := []string{}
	for childName := range ipman.Spec.Children {
		childNames = append(childNames, childName)
	}

	podsWithAnnotation := []*corev1.Pod{}
	for _, p := range podlist.Items {
		if slices.Contains(childNames, p.Annotations[ChildNameAnnotationKey]) {
			podsWithAnnotation = append(podsWithAnnotation, &p)
		}
	}

	// TODO: if it turns out it's taking too long we can reverse sort by
	// timestamp and stop after we reach the first still valid
	maps.DeleteFunc(ipman.Status.PendingIPs, func(ip string, timestamp string) bool {
		ts, err := time.Parse(time.Layout, timestamp)
		if err != nil {
			logger.Error(err, "Malformed timestamp in pending ips", "timestamp", timestamp)
			return true
		}
		contains := slices.ContainsFunc(podsWithAnnotation, func(p *corev1.Pod) bool {
			return p.Annotations[VxlanIpAnnotationKey] == ip
		})
		timePassed := time.Now().After(ts.Add(time.Second * time.Duration(PendingIpTimeoutSeconds)))

		return contains || timePassed
	})

	podIpList := []string{}
	for _, p := range podsWithAnnotation {
		podIpList = append(podIpList, p.Annotations[VxlanIpAnnotationKey])
	}

	for cn, pools := range ipman.Status.FreeIPs {
		if ipman.Status.FreeIPs[cn] == nil {
			ipman.Status.FreeIPs[cn] = map[string][]string{}
		}
		for poolName := range pools {
			if ipman.Status.FreeIPs[cn][poolName] == nil {
				ipman.Status.FreeIPs[cn][poolName] = []string{}
			}
			pendingIps := slices.Collect(maps.Keys(ipman.Status.PendingIPs))
			ipman.Status.FreeIPs[cn][poolName] = slices.DeleteFunc(ipman.Status.FreeIPs[cn][poolName], func(ip string) bool {
				return slices.Contains(pendingIps, ip) || slices.Contains(podIpList, ip)
			})
		}
	}

	p := slices.Collect(maps.Values(ipman.Status.PendingIPs))
	sortedPending := []time.Duration{}
	for _, v := range p {
		// ignored since already checked above
		// maybe combine it if there is a lot of them :TODO
		t, _ := time.Parse(time.Layout, v)
		sortedPending = append(sortedPending, t.Add(time.Second*35).Sub(time.Now()))
	}
	if len(sortedPending) == 0 {
		return nil, nil
	}
	requeueIn := slices.Min(sortedPending)

	return &requeueIn, nil
}

func (r *IpmanReconciler) reconcileIpman(ctx context.Context, req reconcile.Request) error {
	logger := log.FromContext(ctx)

	ipman := &ipmanv1.Ipman{}
	if err := r.Get(ctx, req.NamespacedName, ipman); err != nil {
		logger.Error(err, "Error getting ipman instance")
		return err
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      ipman.Spec.SecretRef.Name,
		Namespace: ipman.Spec.SecretRef.Namespace}, secret)
	if err != nil {
		logger.Error(err, "Failed to find secret", "secretName", ipman.Spec.SecretRef.Name)
		return err
	}

	_, proxyPod, err := r.ensureCharonPod(ctx, secret, ipman)
	if err != nil {
		return fmt.Errorf("failed to ensure Charon pod: %w", err)
	}

	if err := r.Get(ctx, req.NamespacedName, ipman); err != nil {
		logger.Error(err, "Error getting ipman instance to update status in ensuring charon pod")
		return err
	}

	ipman.Status.CharonProxyIP = proxyPod.Status.PodIP
	err = r.Status().Update(ctx, ipman)
	if err != nil {
		logger.Error(err, "Could't update status of ipman to add charon pod ip")
		return err
	}

	for childName, c := range ipman.Spec.Children {
		xfrmPod, err := r.ensureXfrmPod(ctx, &c, ipman.Spec.NodeName, ipman.Status.CharonProxyIP, ipman.Spec.Name)
		if err != nil {
			logger.Error(err, "error creating xfrmpod")
			return err
		}

		if ipman.Status.XfrmGatewayIPs == nil {
			ipman.Status.XfrmGatewayIPs = map[string]string{}
		}
		if ipman.Status.XfrmGatewayIPs[childName] != xfrmPod.Status.PodIP {
			ipman.Status.XfrmGatewayIPs[childName] = xfrmPod.Status.PodIP
			err = r.Status().Update(ctx, ipman)
			if err != nil {
				logger.Error(err, "Error changing status of ipman", "ipman", ipman)
				return err
			}
		}
	}

	return nil
}

func (r *IpmanReconciler) reconcilePod(ctx context.Context, req reconcile.Request) error {
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	err := r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		return fmt.Errorf("error fetching reconciled pod: %w", err)
	}

	logger.Info("Waiting for pod to get assigned ip", "pod", pod.Name)
	for pod.Status.PodIP == "" {
		time.Sleep(1 * time.Second)
		err = r.Get(ctx, req.NamespacedName, pod)
		if err != nil {
			return fmt.Errorf("Couldn't fetch pod while waiting for it's ip to add to bridge fdb: %w", err)
		}
	}
	logger.Info("Pod got assigned ip", "pod", pod.Name, "ip", pod.Status.PodIP)

	vxlanIp, ok := pod.Annotations[VxlanIpAnnotationKey]
	if !ok {
		return fmt.Errorf("Annotation vxlanIp not present")
	}
	ifid, ok := pod.Annotations[InterfaceIdAnnotationKey]
	if !ok {
		return fmt.Errorf("Annotation interfaceid not present")
	}

	ipman := &ipmanv1.Ipman{}
	err = r.Get(
		ctx,
		types.NamespacedName{
			Name:      pod.Annotations[IpmanNameAnnotationKey],
			Namespace: "",
		},
		ipman,
	)
	if err != nil {
		return fmt.Errorf("Couldn't fetch pod while waiting for it's ip to add to bridge fdb: %w", err)
	}

	childName := pod.Annotations[ChildNameAnnotationKey]
	url := fmt.Sprintf("http://%s:8080/addEntry", ipman.Status.XfrmGatewayIPs[childName])
	resp, err := comms.SendPost(url, comms.BridgeFdbRequest{
		CiliumIp:    pod.Status.PodIP,
		VxlanIp:     vxlanIp,
		InterfaceId: ifid,
	})

	if err != nil {
		return fmt.Errorf("Couldn't send post request to add bridge fdb entry for pod: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("Response status code from bridge fdb is not 200: %d", resp.StatusCode)
	}

	return nil
}

func res(rq *time.Duration, times ...time.Duration) ctrl.Result {
	if rq == nil {
		return ctrl.Result{}
	}

	times = append(times, *rq)
	min := slices.Min(times)

	return ctrl.Result{RequeueAfter: min}
}

func (r *IpmanReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rq *time.Duration
	iml := &ipmanv1.IpmanList{}
	err := r.List(ctx, iml)
	if err != nil {
		logger.Error(err, "Error listing ipmen")
		return res(nil), err
	}

	for _, im := range iml.Items {
		success := false
		for !success {
			success = true
			ipman := &ipmanv1.Ipman{}
			err := r.Get(ctx, types.NamespacedName{
				Namespace: im.Namespace,
				Name:      im.Name,
			}, ipman)
			rq, err = r.updateIpmanStatus(ipman, ctx)
			if err != nil {
				logger.Info("Couldn't create free ip list to change status", err)
				success = false
			}
			err = r.Status().Update(ctx, ipman)
			if err != nil {
				logger.Info("Couldn't update ipman status", err)
				success = false
			}
		}
	}

	isIpman, err := r.isReconcilingKindIpman(ctx, req)
	if err != nil {
		logger.Error(err, "Error checking kind of reconciled object")
		return res(rq), err
	}

	if isIpman {
		logger.Info("Reconciling ipman")
		err := r.reconcileIpman(ctx, req)
		if err != nil {
			return res(rq, time.Second*10), nil
		}
		return res(rq), nil
	}

	isPod, err := r.isReconcilingKindPod(ctx, req)
	if err != nil {
		logger.Error(err, "Couldn't check kind of reconciled object")
		return res(rq), err
	}
	if isPod {
		logger.Info("Reconciling pod")
		err := r.reconcilePod(ctx, req)
		if err != nil {
			logger.Error(err, "Error reconciling pod")
			return res(rq), err
		}
		return res(rq), nil
	}

	logger.Info("Reconciling deletion")
	pods := &corev1.PodList{}
	err = r.List(ctx, pods)
	if err != nil {
		return res(rq), fmt.Errorf("Error fetching list of pods when reconciling deletion: %w", err)
	}
	chlidrenNameList := []string{}
	for _, im := range iml.Items {
		for _, c := range im.Spec.Children {
			chlidrenNameList = append(chlidrenNameList, c.Name)
		}
	}
	// something deleted
	if len(iml.Items) == 0 {
		for _, p := range pods.Items {
			if p.Namespace == IpmanNamespace {
				if val, ok := p.Annotations[ChildNameAnnotationKey]; ok && val != "" {
					err = r.Delete(ctx, &p)
					if err != nil {
						logger.Error(err, "Error deleting pod since there are no ipmen", "podname", p.Name)
					}
				}
				s := strings.Split(p.Name, "-")
				if s[0] == "charon" && s[1] == "pod" && p.Namespace == IpmanNamespace {
					err = r.Delete(ctx, &p)
					if err != nil {
						logger.Error(err, "Error deleting charon pod since there are no ipmen", "podname", p.Name)
					}
				}

			}
		}
	} else {
		for _, p := range pods.Items {
			if p.Namespace != IpmanNamespace {
				continue
			}

			annotation, ok := p.Annotations[ChildNameAnnotationKey]
			if ok && !slices.Contains(chlidrenNameList, annotation) {
				err = r.Delete(ctx, &p)
				if err != nil {
					logger.Error(err, "Error deleting pod since there are no ipmen", "podname", p.Name)
				}
			}
		}
	}
	return res(rq), nil
}

var annotationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return hasIpmanAnnotation(e.Object)
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return hasIpmanAnnotation(e.ObjectNew)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return hasIpmanAnnotation(e.Object) && e.Object.GetNamespace() != IpmanNamespace
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return hasIpmanAnnotation(e.Object)
	},
}

func hasIpmanAnnotation(o client.Object) bool {
	_, ok := o.GetAnnotations()[VxlanIpAnnotationKey]
	return ok
}

func (r *IpmanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ipmanv1.Ipman{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(annotationPredicate)).
		Complete(r)
}
