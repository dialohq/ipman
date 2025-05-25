package controller

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"log/slog"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// rke2 requires seccomp profile set to runtime default or localhost
func (r *IPSecConnectionReconciler) createDefaultSecurityContext() *corev1.SecurityContext {
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
			Add: []corev1.Capability{"NET_ADMIN", "NET_RAW"},
		},
	}
}

func (r *IPSecConnectionReconciler) createCharonDaemonSecurityContext() *corev1.SecurityContext {
	def := r.createDefaultSecurityContext()
	def.Capabilities.Add = []corev1.Capability{"NET_ADMIN", "NET_RAW", "NET_BIND_SERVICE"}
	return def

}

func (r *IPSecConnectionReconciler) createNetAdminSecurityContext() *corev1.SecurityContext {
	def := r.createDefaultSecurityContext()
	return def
}

func waitForPodReady(pod *corev1.Pod,
	nsn types.NamespacedName,
	get func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error) (*corev1.Pod, error) {

	ctx := context.Background()
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	for pod.Status.Phase != "Running" {
		err := get(ctx, nsn, pod)
		if err != nil {
			logger.Error("Error fetching charon pod while checking for container readiness", "msg", err, "pod", pod.Name)
		}
		time.Sleep(1 * time.Second)
	}

	for pod.Status.PodIP == "" {
		err := get(ctx, nsn, pod)
		if err != nil && !apierrors.IsNotFound(err) {
			logger.Error("Error fetching charon pod while waiting for ip allocation", "msg", err)
			return nil, err
		}
		time.Sleep(1 * time.Second)
	}
	return pod, nil
}
