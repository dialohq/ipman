package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

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

var (
	CharonPodName                 = "charon-pod"
	// CharonApiSocketVolumeHostPath = "/var/run/restctl"
	CharonApiSocketVolumePath     = "/restctlsock/"
	CharonApiSocketVolumeName     = "restctl"
	CharonSocketVolume            = "charon-volume"
	CharonConfVolume              = "charon-conf"
	CharonContainerImage          = "plan9better/strongswan-charon:0.0.1"
	XfrmPodName                   = "xfrm-pod"
)

type IpmanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}


func (r *IpmanReconciler) isReconcilingKindIpman(ctx context.Context, req reconcile.Request) (bool, error){
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

// returns time after which to requeue ipman
func (r *IpmanReconciler) updateIpmanStatus(ipman *ipmanv1.Ipman, ctx context.Context) (*time.Duration, error){
	logger := log.FromContext(ctx)
	
	if ipman.Status.FreeIPs == nil {
		ipman.Status.FreeIPs = map[string]map[string][]string{}
	}

	for _, c := range ipman.Spec.Children {
		if ipman.Status.FreeIPs[c.Name] == nil {
			ipman.Status.FreeIPs[c.Name] = map[string][]string{}
		}
		for name, ips := range c.IpPools {
			if ipman.Status.FreeIPs[c.Name][name] == nil {
				ipman.Status.FreeIPs[c.Name][name] = []string{}
			}

			ipman.Status.FreeIPs[c.Name][name] = slices.Clone(ips)
		}
	}

	podlist := &corev1.PodList{}
	err := r.List(ctx, podlist)
	if err != nil {
		logger.Error(err, "Error listing pods to check IPs")
		return nil, err
	}

	childNames := []string{}
	for _, c := range ipman.Spec.Children {
		childNames = append(childNames, c.Name)
	}

	podsWithAnnotation := []*corev1.Pod{}
	for _, p := range podlist.Items {
		if slices.Contains(childNames, p.Annotations["ipman.dialo.ai/childName"]) {
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
		contains := slices.ContainsFunc(podsWithAnnotation, func (p *corev1.Pod) bool {
			return p.Annotations["ipman.dialo.ai/vxlanIp"] == ip
		})
		timePassed := time.Now().After(ts.Add(time.Second * 35))

		return contains || timePassed
	})

	podIpList := []string{}
	for _, p := range podsWithAnnotation {
		podIpList = append(podIpList, p.Annotations["ipman.dialo.ai/vxlanIp"])
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
			ipman.Status.FreeIPs[cn][poolName] = slices.DeleteFunc(ipman.Status.FreeIPs[cn][poolName], func (ip string) bool {
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
		sortedPending = append(sortedPending, t.Add(time.Second * 35).Sub(time.Now()))
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
		Name: ipman.Spec.SecretRef.Name,
		Namespace: ipman.Spec.SecretRef.Namespace}, secret);
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

	for _, c := range ipman.Spec.Children {
		xfrmPod, err := r.ensureXfrmPod(ctx, &c, ipman.Spec.NodeName, ipman.Status.CharonProxyIP)
		if err != nil {
			logger.Error(err, "error creating xfrmpod")
			return err
		}

		if ipman.Status.XfrmGatewayIPs == nil {
			ipman.Status.XfrmGatewayIPs = map[string]string{}
		}
		if ipman.Status.XfrmGatewayIPs[c.Name] != xfrmPod.Status.PodIP{
			ipman.Status.XfrmGatewayIPs[c.Name] = xfrmPod.Status.PodIP
			err = r.Status().Update(ctx, ipman)
			if err != nil {
				logger.Error(err, "Error changing status of ipman","ipman", ipman)
				return err
			}
		}
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
		ipman := &ipmanv1.Ipman{}
		err := r.Get(ctx,types.NamespacedName{
			Namespace: im.Namespace,
			Name: im.Name,
		} ,ipman)
		rq, err = r.updateIpmanStatus(ipman, ctx)
		if err != nil {
			logger.Error(err, "Error creating free ip list to change status")
			return res(rq), err
		}
		err = r.Status().Update(ctx, ipman)
		if err != nil {
			logger.Error(err, "Couldn't update ipman status")
			return res(rq), err
		}
	}

	isIpman, err := r.isReconcilingKindIpman(ctx, req)
	if err != nil {
		logger.Error(err, "Error checking kind of reconciled object")
	}

	if isIpman {
		err := r.reconcileIpman(ctx, req)
		if err != nil {
			return res(rq, time.Second * 10), nil
		}
		return res(rq), nil
	}

	pod := &corev1.Pod{}
	err = r.Get(ctx, req.NamespacedName, pod)
	if err != nil {
		logger.Error(err, "Reconciled pod not found")
		return res(rq), err
	}

	logger.Info("Waiting for pod to get assigned ip")
	for pod.Status.PodIP == "" {
		time.Sleep(1 * time.Second)
		err = r.Get(ctx, req.NamespacedName, pod)
		if err != nil {
			logger.Error(err, "Couldn't fetch pod while waiting for it's ip to add to bridge fdb")
			return res(rq), err
		}
	}
	logger.Info("Pod got assigned ip", "ip", pod.Status.PodIP)

	vxlanIp, ok := pod.Annotations["ipman.dialo.ai/vxlanIp"]
	if !ok {
		logger.Info("Annotation vxlanIp not present")
		return res(rq), nil
	}
	ifid, ok := pod.Annotations["ipman.dialo.ai/interfaceId"]
	if !ok {
		logger.Info("Annotation interfaceid not present")
		return res(rq), nil
	}

	ipman := &ipmanv1.Ipman{}
	err = r.Get(
		ctx,
		types.NamespacedName{
			Name: pod.Annotations["ipman.dialo.ai/ipmanName"],
			Namespace: "",
		},
		ipman,
	)
	if err != nil {
		logger.Error(err, "Error fetching ipman instance while reconciling pod")
		return res(rq), err
	}

	childName := pod.Annotations["ipman.dialo.ai/childName"]
	url := fmt.Sprintf("http://%s:8080/addEntry", ipman.Status.XfrmGatewayIPs[childName])
	resp, err := comms.SendPost(url, comms.BridgeFdbRequest{
		CiliumIp: pod.Status.PodIP,
		VxlanIp: vxlanIp,
		InterfaceId: ifid,
	})

	if err != nil {
		logger.Error(err, "Couldn't send post request to add bridge fdb entry for pod", "pod", pod.Name)
		return res(rq), nil
	}

	if resp.StatusCode != 200 {
		logger.Error(err, "Response status code from bridge fdb is not 200", "pod", pod.Name, "code", resp.StatusCode)
		return res(rq), nil
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
		return hasIpmanAnnotation(e.Object)
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return hasIpmanAnnotation(e.Object)
	},
}

func hasIpmanAnnotation(o client.Object) bool {
	_, ok := o.GetAnnotations()["ipman.dialo.ai/vxlanIp"]
	return ok
}

func (r *IpmanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ipmanv1.Ipman{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(annotationPredicate)).
		Complete(r)
}
