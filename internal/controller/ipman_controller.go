package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
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

type Envs struct {
	NamespaceName     string
	HostSocketsPath   string
	XfrminionImage    string
	VxlandlordImage   string
	RestctlImage      string
	CaddyImage        string
	CharonDaemonImage string
}

type IPSecConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Env    Envs
}

func (r *IPSecConnectionReconciler) isReconcilingKindIPSecConnection(ctx context.Context, req reconcile.Request) (bool, error) {
	im := &ipmanv1.IPSecConnection{}
	err := r.Get(ctx, req.NamespacedName, im)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *IPSecConnectionReconciler) isReconcilingKindPod(ctx context.Context, req reconcile.Request) (bool, error) {
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
			return p.Annotations[ipmanv1.AnnotationVxlanIp] == ip
		})
		timePassed := time.Now().After(ts.Add(time.Second * time.Duration(ipmanv1.ReconcilerPendingIpsTimeoutSeconds)))

		return contains || timePassed
	})

	podIpList := []string{}
	for _, p := range podsWithAnnotation {
		podIpList = append(podIpList, p.Annotations[ipmanv1.AnnotationVxlanIp])
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
		sortedPending = append(sortedPending, t.Add(time.Second*ipmanv1.ReconcilerPendingIpsTimeoutSeconds).Sub(time.Now()))
	}
	if len(sortedPending) == 0 {
		return nil, nil
	}
	requeueIn := slices.Min(sortedPending)

	return &requeueIn, nil
}

func (r *IPSecConnectionReconciler) reconcileIPSecConnection(ctx context.Context, req reconcile.Request) error {
	logger := log.FromContext(ctx)

	ipsecconnection := &ipmanv1.IPSecConnection{}
	if err := r.Get(ctx, req.NamespacedName, ipsecconnection); err != nil {
		logger.Error(err, "Error getting ipsecconnection instance")
		return err
	}

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      ipsecconnection.Spec.SecretRef.Name,
		Namespace: ipsecconnection.Spec.SecretRef.Namespace}, secret)
	if err != nil {
		logger.Error(err, "Failed to find secret", "secretName", ipsecconnection.Spec.SecretRef.Name)
		return err
	}

	_, proxyPod, err := r.ensureCharonPod(ctx, ipsecconnection)
	if err != nil {
		return fmt.Errorf("failed to ensure Charon pod: %w", err)
	}

	if err := r.Get(ctx, req.NamespacedName, ipsecconnection); err != nil {
		logger.Error(err, "Error getting ipsecconnection instance to update status in ensuring charon pod")
		return err
	}

	ipsecconnection.Status.CharonProxyIP = proxyPod.Status.PodIP
	err = r.Status().Update(ctx, ipsecconnection)
	if err != nil {
		logger.Error(err, "Could't update status of ipsecconnection to add charon pod ip")
		return err
	}

	for childName, c := range ipsecconnection.Spec.Children {
		xfrmPod, err := r.ensureXfrmPod(ctx, &c, ipsecconnection.Spec.NodeName, ipsecconnection.Status.CharonProxyIP, ipsecconnection.Spec.Name, ipsecconnection.Name)
		if err != nil {
			logger.Error(err, "error creating xfrmpod")
			return err
		}

		if ipsecconnection.Status.XfrmGatewayIPs == nil {
			ipsecconnection.Status.XfrmGatewayIPs = map[string]string{}
		}
		if ipsecconnection.Status.XfrmGatewayIPs[childName] != xfrmPod.Status.PodIP {
			ipsecconnection.Status.XfrmGatewayIPs[childName] = xfrmPod.Status.PodIP
			err = r.Status().Update(ctx, ipsecconnection)
			if err != nil {
				logger.Error(err, "Error changing status of ipsecconnection", "ipsecconnection", ipsecconnection)
				return err
			}
		}
	}

	return nil
}

func (r *IPSecConnectionReconciler) reconcilePod(ctx context.Context, req reconcile.Request) error {
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
			return fmt.Errorf("Couldn't fetch pod while reconciling: %w", err)
		}
	}
	logger.Info("Pod got assigned ip", "pod", pod.Name, "ip", pod.Status.PodIP)

	vxlanIp, ok := pod.Annotations[ipmanv1.AnnotationVxlanIp]
	if !ok {
		return fmt.Errorf("Annotation vxlanIp not present")
	}
	ifid, ok := pod.Annotations[ipmanv1.AnnotationIntefaceId]
	if !ok {
		return fmt.Errorf("Annotation interfaceid not present")
	}

	ipsecconnection := &ipmanv1.IPSecConnection{}
	err = r.Get(
		ctx,
		types.NamespacedName{
			Name:      pod.Annotations[ipmanv1.AnnotationIpmanName],
			Namespace: "",
		},
		ipsecconnection,
	)
	if err != nil {
		return fmt.Errorf("Couldn't fetch ipsecconnection while reconciling pod: %w", err)
	}

	childName := pod.Annotations[ipmanv1.AnnotationChildName]
	url := fmt.Sprintf("http://%s:8080/addEntry", ipsecconnection.Status.XfrmGatewayIPs[childName])
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

func (r *IPSecConnectionReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rq *time.Duration
	iml := &ipmanv1.IPSecConnectionList{}
	err := r.List(ctx, iml)
	if err != nil {
		logger.Error(err, "Error listing ipsecconnections")
		return res(nil), err
	}

	for _, im := range iml.Items {
		success := false
		for !success {
			success = true
			ipsecconnection := &ipmanv1.IPSecConnection{}
			err := r.Get(ctx, types.NamespacedName{
				Namespace: im.Namespace,
				Name:      im.Name,
			}, ipsecconnection)
			rq, err = r.updateIPSecConnectionStatus(ipsecconnection, ctx)
			if err != nil {
				logger.Info("Couldn't create free ip list to change status", err)
				success = false
			}
			err = r.Status().Update(ctx, ipsecconnection)
			if err != nil {
				logger.Info("Couldn't update ipsecconnection status", err)
				success = false
			}
		}
	}

	isIPSecConnection, err := r.isReconcilingKindIPSecConnection(ctx, req)
	if err != nil {
		logger.Error(err, "Error checking kind of reconciled object")
		return res(rq), err
	}

	if isIPSecConnection {
		logger.Info("Reconciling ipsecconnection")
		err := r.reconcileIPSecConnection(ctx, req)
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
			if p.Namespace == r.Env.NamespaceName {
				if val, ok := p.Annotations[ipmanv1.AnnotationChildName]; ok && val != "" {
					err = r.Delete(ctx, &p)
					if err != nil {
						logger.Error(err, "Error deleting pod since there are no ipsecconnections", "podname", p.Name)
					}
				}

				s := strings.Split(p.Name, "-")
				rpn := strings.Split(ipmanv1.RestctlPodName, "-")
				if s[0] == rpn[0] && s[1] == rpn[1] && p.Namespace == r.Env.NamespaceName {
					err = r.Delete(ctx, &p)
					if err != nil {
						logger.Error(err, "Error deleting restctl pod since there are no ipmen", "podname", p.Name)
					}
				}

				cpn := strings.Split(ipmanv1.CharonPodName, "-")
				if s[0] == cpn[0] && s[1] == cpn[1] && p.Namespace == r.Env.NamespaceName {
					err = r.Delete(ctx, &p)
					if err != nil {
						logger.Error(err, "Error deleting charon pod since there are no ipsecconnections", "podname", p.Name)
					}
				}

			}
		}
	} else {
		for _, p := range pods.Items {
			if p.Namespace != r.Env.NamespaceName {
				continue
			}

			annotation, ok := p.Annotations[ipmanv1.AnnotationChildName]
			if ok && !slices.Contains(chlidrenNameList, annotation) {
				err = r.Delete(ctx, &p)
				if err != nil {
					logger.Error(err, "Error deleting pod since there are no ipsecconnections", "podname", p.Name)
				}
			}
		}
	}
	return res(rq), nil
}

func hasIpmanAnnotation(o client.Object) bool {
	_, ok := o.GetAnnotations()[ipmanv1.AnnotationVxlanIp]
	return ok
}

func (r *IPSecConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var annotationPredicate = predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return hasIpmanAnnotation(e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return hasIpmanAnnotation(e.ObjectNew)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return hasIpmanAnnotation(e.Object) && e.Object.GetNamespace() != r.Env.NamespaceName
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return hasIpmanAnnotation(e.Object)
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&ipmanv1.IPSecConnection{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(annotationPredicate)).
		Complete(r)
}
