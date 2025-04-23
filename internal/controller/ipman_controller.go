package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
)

type IpmanReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Config *ipmanv1.Ipman
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
	_, ok := o.GetAnnotations()["Ipmanaged"]
	return ok
}

func (r *IpmanReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting reconciler", "object", req.Name)

	// TODO: branch of execution
	// when there is no config
	// and someone creates a pod
	if r.Config == nil {
		config, err := r.getIpmanConfig(ctx, req)
		if err != nil {
			logger.Error(err, "Failed to get ipman config, retrying in 10s")
			return ctrl.Result{RequeueAfter: time.Duration(10 * 10 * time.Second)}, nil
		}
		r.Config = config
	}

	// check if config is changed
	nsn := types.NamespacedName{
		Namespace: r.Config.Namespace,
		Name:      r.Config.Name,
	}
	cfg := &ipmanv1.Ipman{}
	err := r.Get(ctx, nsn, cfg)
	if err != nil {
		logger.Error(err, "Error fetching Ipman to compare with last known version")
		return ctrl.Result{}, err
	}
	logger.Info("Fetched ipman instance", "cfg", cfg)

	if !r.Config.Spec.DeepEqual(cfg.Spec) {
		logger.Info("Newer config found, replacing and requeuing")
		r.Config = cfg
		// TODO: handle changes
		return ctrl.Result{Requeue: true}, nil
	}

	// TODO: make it fail gracefully
	// when there is a config with no
	// secret 
	secret, err := r.getSecret(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get secret: %w in namespace %s", err, ctx)
	}

	charonPod, err := r.ensureCharonPod(ctx, req, secret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Charon pod: %w", err)
	}

	if charonPod.Status.PodIP == "" {
		logger.Info("charon pod IP not yet allocated, trying again in 30s")

		return ctrl.Result{RequeueAfter: time.Duration(time.Second * 30)}, nil
	}
	logger.Info("Charon pod has an ip", "ip", charonPod.Status.PodIP)

	// individual child connections
	charonPodAlive := charonPodHealthCheck(ctx, charonPod.Status.PodIP)
	if !charonPodAlive {
		logger.Info("Charon pod failed health check, trying again in 30s")
		return ctrl.Result{RequeueAfter: time.Duration(time.Second * 30)}, nil
	}
	logger.Info("Charon pod is alive and well")

	return ctrl.Result{}, nil

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

func (r *IpmanReconciler) getIpmanConfig(ctx context.Context, req ctrl.Request) (*ipmanv1.Ipman, error) {
	logger := log.FromContext(ctx)
	var config ipmanv1.Ipman

	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		logger.Error(err, "Failed to get Ipman config")
		return nil, err
	}

	return &config, nil
}

func (r *IpmanReconciler) getSecret(ctx context.Context) (*corev1.Secret, error) {
	logger := log.FromContext(ctx)
	var secret corev1.Secret

	nsn := types.NamespacedName{
		Name:      r.Config.Spec.SecretRef.Name,
		Namespace: r.Config.Spec.SecretRef.Namespace,
	}

	if err := r.Get(ctx, nsn, &secret); err != nil {
		logger.Error(err, "Failed to find secret", "secretName", nsn.Name)
		return nil, err
	}

	return &secret, nil
}

func (r *IpmanReconciler) ensureCharonPod(ctx context.Context, req ctrl.Request, secret *corev1.Secret) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)

	var charonPod corev1.Pod
	nsn := types.NamespacedName{
		Name:      CharonPodName,
		Namespace: "ims",
	}

	err := r.Get(ctx, nsn, &charonPod)
	if apierrors.IsNotFound(err) {
		logger.Info("Charon pod not found, creating", "podName", CharonPodName)

		charonPod := r.createCharonPod(req, secret)
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

func (r *IpmanReconciler) createCharonPod(req ctrl.Request, secret *corev1.Secret) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CharonPodName,
			// TODO: handle namespace well
			Namespace: "ims",
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: CharonSocketVolume},
				{Name: CharonConfVolume},
			},
			HostNetwork: true,
			Containers: []corev1.Container{
				r.createCharonDaemonContainer(),
				r.createRestCtlContainer(),
			},
			InitContainers: []corev1.Container{
				r.createConfInitContainer(secret),
			},
		},
	}
}

func (r *IpmanReconciler) createCharonDaemonContainer() corev1.Container {
	return corev1.Container{
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
		Name:  "restctl",
		Image: "plan9better/restctl",
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonSocketVolume, MountPath: "/var/run/"},
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
		},
		SecurityContext: r.createNetAdminSecurityContext(),
	}
}

func (r *IpmanReconciler) createConfInitContainer(secret *corev1.Secret) corev1.Container {
	return corev1.Container{
		Name:  "create-conf",
		Image: "busybox:latest",
		Command: []string{
			"sh", "-c",
			fmt.Sprintf("echo '%s' > /etc/swanctl/swanctl.conf",
				r.Config.Spec.SerializeToConf(101, string(secret.Data["psk"]))),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
		},
		SecurityContext: r.createDefaultSecurityContext(),
	}
}

// rke2 requires seccopm profile set to runtime default or localhost
func (r *IpmanReconciler) createDefaultSecurityContext() *corev1.SecurityContext {
	//TODO: figure out exactly which
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
