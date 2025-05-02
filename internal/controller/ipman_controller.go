package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
)

var (
	CharonPodName        = "charon-pod"
	CharonSocketVolume   = "charon-volume"
	CharonConfVolume     = "charon-conf"
	CharonContainerImage = "plan9better/strongswan-charon:0.0.1"
	XfrmPodName          = "xfrm-pod"
)

type IpmanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
	_, ok := o.GetAnnotations()["ipman.dialo.ai/childName"]
	return ok
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

func (r *IpmanReconciler) createIpmanFreeIps(ipman *ipmanv1.Ipman, ctx context.Context) (map[string][]string, error){
	logger := log.FromContext(ctx)
	
	childNames := []string{}

	status := map[string][]string{}
	for _, child := range ipman.Spec.Children {
		status[child.Name] = slices.Clone(child.Ip_pool)
		childNames = append(childNames, child.Name)
	}

	podlist := &corev1.PodList{}
	err := r.List(ctx, podlist)
	if err != nil {
		logger.Error(err, "Error listing pods to check IPs")
		return nil, err
	}
	if len(podlist.Items) == 0 {
		return status, nil
	}

	podsWithAnnotation := []*corev1.Pod{}
	for _, p := range podlist.Items {
		if slices.Contains(childNames, p.Annotations["ipman.dialo.ai/childName"]) {
			podsWithAnnotation = append(podsWithAnnotation, &p)
		}
	}
	if len(podsWithAnnotation) == 0 {
		return status, nil
	}

	for _, pod := range podsWithAnnotation {
		childName := pod.Annotations["ipman.dialo.ai/childName"]
		vxlanIp := pod.Annotations["ipman.dialo.ai/vxlan"] 
		status[childName] = slices.DeleteFunc(status[childName], func(ip string)bool{
			return ip == vxlanIp
		})
	}

	return status, nil
}

func (r *IpmanReconciler) reconcileIpman(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ipman")

	ipman := &ipmanv1.Ipman{} 
	if err := r.Get(ctx, req.NamespacedName, ipman); err != nil {
		logger.Error(err, "Error getting ipman instance")
		return ctrl.Result{}, err
	}

	// TODO: make it fail gracefully
	// when there is a config with no
	// secret 
	secret, err := r.getSecret(ctx, ipman)
	if err != nil {
		logger.Error(err, "Error, couldn't get secret")
		return ctrl.Result{RequeueAfter: time.Duration(time.Second * 15)}, err
	}

	charonPod, err := r.ensureCharonPod(ctx, secret, ipman)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Charon pod: %w", err)
	}

	if charonPod.Status.PodIP == "" {
		logger.Info("charon pod IP not yet allocated, trying again in 10s")

		return ctrl.Result{RequeueAfter: time.Duration(time.Second * 10)}, nil
	}

	// individual child connections
	charonPodAlive := charonPodHealthCheck(ctx, charonPod.Status.PodIP)
	if !charonPodAlive {
		logger.Info("Charon pod failed health check, trying again in 30s")
		return ctrl.Result{RequeueAfter: time.Duration(time.Second * 30)}, nil
	}
	logger.Info("Charon pod is alive and well")

	for _, c := range ipman.Spec.Children {
		xfrmPod, err := r.ensureXfrmPod(ctx, &c)
		if err != nil {
			logger.Error(err, "error creating xfrmpod")
		}
		logger.Info("xfrm pod exists", "pod", xfrmPod)

		if xfrmPod.Status.HostIP == "" {
			logger.Info("xfrm pod is not assigned an ip yet, trying again in 10s")
			return ctrl.Result{RequeueAfter: time.Duration(time.Second * 10)}, nil
		}

		if ipman.Status.XfrmGatewayIP != xfrmPod.Status.HostIP{
			ipman.Status.XfrmGatewayIP = xfrmPod.Status.HostIP
			err = r.Status().Update(ctx, ipman)
			if err != nil {
				logger.Error(err, "Error changing status of ipman","ipman", ipman)
				return ctrl.Result{RequeueAfter: time.Duration(10 * time.Second)}, nil
			}
		}
		
	}

	return ctrl.Result{}, nil
}

func (r *IpmanReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	iml := &ipmanv1.IpmanList{}
	err := r.List(ctx, iml)
	if err != nil {
		logger.Error(err, "Error listing ipmen")
		return ctrl.Result{}, err
	}

	for _, im := range iml.Items {
		ipman := &ipmanv1.Ipman{}
		err := r.Get(ctx,types.NamespacedName{
			Namespace: im.Namespace,
			Name: im.Name,
		} ,ipman)
		freeIps, err := r.createIpmanFreeIps(ipman, ctx)
		if err != nil {
			logger.Error(err, "Error creating free ip list to change status")
			return ctrl.Result{}, err
		}
		ipman.Status.FreeIPs = freeIps
		err = r.Status().Update(ctx, ipman)
		if err != nil {
			logger.Error(err, "Couldn't update ipman status")
			return ctrl.Result{}, err
		}
	}

	isIpman, err := r.isReconcilingKindIpman(ctx, req)
	if err != nil {
		logger.Error(err, "Error checking kind of reconciled object")
	}

	if isIpman {
		r, err := r.reconcileIpman(ctx, req)
		return r, err
	}

	return ctrl.Result{}, nil
}

func (r *IpmanReconciler) ensureXfrmPod(ctx context.Context, c *ipmanv1.Child) (*corev1.Pod, error){
	logger := log.FromContext(ctx)

	var xfrmPod corev1.Pod
	nsn := types.NamespacedName{
		Name:      XfrmPodName,
		Namespace: "ims",
	}

	err := r.Get(ctx, nsn, &xfrmPod)
	if apierrors.IsNotFound(err) {
		logger.Info("xfrm pod not found, creating", "podName", XfrmPodName)

		xfrmPod := r.createXfrmPod(c)
		if err := r.Create(ctx, xfrmPod); err != nil {
			logger.Error(err, "Failed to create xfrm pod")
			return nil, err
		}

		logger.Info("Successfully created xfrm pod", "podName", XfrmPodName, "ip", xfrmPod.Status.PodIP)
	} else if err != nil {
		logger.Error(err, "Error checking xfrm pod existence")
		return nil, err
	}

	return &xfrmPod, nil

}

func (r *IpmanReconciler)createXfrmPod(c *ipmanv1.Child) *corev1.Pod{
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      XfrmPodName,
			Namespace: "ims",
		},
		Spec: corev1.PodSpec{
			HostPID: true,
			SecurityContext: &corev1.PodSecurityContext{
				Sysctls: []corev1.Sysctl{
					{
						Name: "net.ipv4.ip_forward",
						Value: "1",
					},
					{
						Name: "net.ipv4.conf.all.rp_filter",
						Value: "0",
					},
					{
						Name: "net.ipv4.conf.default.rp_filter",
						Value: "0",
					},
					{
						Name: "net.ipv4.conf.all.arp_filter",
						Value: "1",
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name: "xfrm-container", 
					Image: "plan9better/xfrminion-long:latest",
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: r.createNetAdminSecurityContext(),
					Env: []corev1.EnvVar{
						{
							Name: "IF_ID",
							Value: strconv.FormatInt(int64(c.If_id), 10),
						},
					},
				},
			},
			InitContainers: []corev1.Container{
				{
					Name: "iface-request",
					Image: "plan9better/xfrminion:latest",
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: r.createNetAdminSecurityContext(),
					Env: []corev1.EnvVar{
						{
							Name: "CHILD_NAME",
							Value: c.Name,
						},
					},
				},
			},
		},
	}

}

func charonPodHealthCheck(ctx context.Context, podIp string) bool {
	// TODO:
	// Make this more involved not just
	// p1ng -> p0ng but maybe check presence
	// of config or smth more, also add
	// retrying with timeout for errors
	logger := log.FromContext(ctx)
	resp, err := http.Get(fmt.Sprintf("http://%s:8080/p1ng", podIp))
	if err != nil {
		logger.Error(err, "Error making request to check health of charon pod")
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(err, "Error reading charon pod health check response, read error")
	}

	if resp.StatusCode == 200 && string(body) == "p0ng" {
		return true
	}

	return false
}

func (r *IpmanReconciler) getSecret(ctx context.Context, ipman *ipmanv1.Ipman) (*corev1.Secret, error) {
	logger := log.FromContext(ctx)
	var secret corev1.Secret

	nsn := types.NamespacedName{
		Name:      ipman.Spec.SecretRef.Name,
		Namespace: ipman.Spec.SecretRef.Namespace,
	}

	if err := r.Get(ctx, nsn, &secret); err != nil {
		logger.Error(err, "Failed to find secret", "secretName", nsn.Name)
		return nil, err
	}

	return &secret, nil
}

func (r *IpmanReconciler) ensureCharonPod(ctx context.Context, secret *corev1.Secret, ipman *ipmanv1.Ipman) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)
	nsn := types.NamespacedName {
		Name: fmt.Sprintf("%s-configmap", ipman.Name),
		Namespace: "ims",
	}
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, nsn, cm)
	if err != nil {
		if client.IgnoreNotFound(err) != nil{
			logger.Error(err, "Could't fetch config map", "configmap", fmt.Sprintf("%s-configmap", ipman.Name))
			return nil, err
		}

		logger.Info("config map not found, creating")
		d := map[string]string{}
		for _, c := range ipman.Spec.Children {
			b, err := json.Marshal(c) 
			if err != nil {
				logger.Error(err, "Error marshalling child", "child", c)
				return nil, err
			}
			d[c.Name] = string(b)
		}
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: nsn.Name,
				Namespace: nsn.Namespace,
			},
			Data: d,
		}
		err = r.Create(ctx, cm)
		if err != nil {
			logger.Error(err, "Error creating config map", "configmap", *cm)
			return nil, err
		}
		logger.Info("config map created")
	}
	// TODO: if found, update

	var charonPod corev1.Pod
	nsn = types.NamespacedName{
		Name:      CharonPodName,
		Namespace: "ims",
	}

	err = r.Get(ctx, nsn, &charonPod)
	if apierrors.IsNotFound(err) {
		logger.Info("Charon pod not found, creating", "podName", CharonPodName)

		charonPod := r.createCharonPod(secret, ipman, cm)
		if err := r.Create(ctx, charonPod); err != nil {
			logger.Error(err, "Failed to create Charon pod")
			return nil, err
		}

		logger.Info("Successfully created Charon pod", "podName", CharonPodName, "ip", charonPod.Status.PodIP)
	} else if err != nil {
		logger.Error(err, "Error checking Charon pod existence")
		return nil, err
	}

	return &charonPod, nil
}

func (r *IpmanReconciler) createCharonPod(secret *corev1.Secret, ipman *ipmanv1.Ipman, cm *corev1.ConfigMap) *corev1.Pod {
	vs := corev1.VolumeSource{
		ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: cm.Name,
			},
		},
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CharonPodName,
			Namespace: "ims",
			Labels: map[string]string{
				"ipserviced": "true", // to get picked up by the service
			},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: CharonSocketVolume},
				{Name: CharonConfVolume},
				{
					Name: "charon-conn",
					VolumeSource: vs,
				},
			},
			HostNetwork: true,
			HostPID: true,
			Containers: []corev1.Container{
				r.createCharonDaemonContainer(),
				r.createRestCtlContainer(),
			},
			InitContainers: []corev1.Container{
				r.createConfInitContainer(secret, ipman),
			},
		},
	}
}

func (r *IpmanReconciler) createCharonDaemonContainer() corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:  "charondaemon",
		Image: CharonContainerImage,
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonSocketVolume, MountPath: "/var/run/"},
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
		},
		SecurityContext: r.createCharonDaemonSecurityContext(),
	}
}

func (r *IpmanReconciler) createRestCtlContainer() corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:  "restctl",
		Image: "plan9better/restctl",
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonSocketVolume, MountPath: "/var/run/"},
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
			{Name: "charon-conn", MountPath: "/etc/charon-conn"},
		},
		SecurityContext: r.createNetAdminSecurityContext(),
	}
}

func (r *IpmanReconciler) createConfInitContainer(secret *corev1.Secret, ipman *ipmanv1.Ipman) corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:  "create-conf",
		Image: "busybox:latest",
		Command: []string{
			"sh", "-c",
			fmt.Sprintf("echo '%s' > /etc/swanctl/swanctl.conf",
				ipman.Spec.SerializeToConf(string(secret.Data["psk"]))),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
		},
		SecurityContext: r.createDefaultSecurityContext(),
	}
}

// rke2 requires seccomp profile set to runtime default or localhost
func (r *IpmanReconciler) createDefaultSecurityContext() *corev1.SecurityContext {
	// TODO: figure out exactly which
	// container require what and make
	// this more specific. charon pod
	// needs to run as root to load
	// plugins 
	privEsc := false
	return &corev1.SecurityContext{
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},

		AllowPrivilegeEscalation: &privEsc,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

func (r *IpmanReconciler) createCharonDaemonSecurityContext() *corev1.SecurityContext{
	def := r.createDefaultSecurityContext()
	def.Capabilities.Add = []corev1.Capability{"NET_ADMIN", "NET_RAW", "NET_BIND_SERVICE"}
	return def

}

func (r *IpmanReconciler) createNetAdminSecurityContext() *corev1.SecurityContext {
	def := r.createDefaultSecurityContext()
	def.Capabilities.Add = []corev1.Capability{"NET_ADMIN"}
	return def
}

func (r *IpmanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO: remember to get rid of this watches
	// if it turns out we don't do anything with
	// them in here
	return ctrl.NewControllerManagedBy(mgr).
		For(&ipmanv1.Ipman{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(annotationPredicate)).
		Complete(r)
}
