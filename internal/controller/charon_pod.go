package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *IpmanReconciler) charonPodNsn(ipmanName string) types.NamespacedName {
	ns := r.Env.NamespaceName
	nameEnv := ipmanv1.CharonPodName

	name := strings.Join([]string{nameEnv, ipmanName}, "-")
	return types.NamespacedName{
		Name:      name,
		Namespace: ns,
	}
}

func (r *IpmanReconciler) ensureCharonPod(ctx context.Context, ipman *ipmanv1.Ipman) (*corev1.Pod, *corev1.Pod, error) {
	logger := log.FromContext(ctx)

	list := &ipmanv1.IpmanList{}
	err := r.List(ctx, list)
	if err != nil {
		return nil, nil, fmt.Errorf("Couldn't fetch list of ipmen to reload charon pod: %w", err)
	}
	cdl := []ipmanv1.ConnData{}
	for _, im := range list.Items {
		sec := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{
			Name:      ipman.Spec.SecretRef.Name,
			Namespace: ipman.Spec.SecretRef.Namespace}, sec)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to find secret(%s): %w", "secretName", err)
		}
		cdl = append(cdl, ipmanv1.ConnData{Secret: string(sec.Data[im.Spec.SecretRef.Key]), Ipman: im})
		logger.Info("Found secret for ipman", "secret", sec, "ipman", im, "cdls", cdl)
	}

	serializedConfig := ipman.Spec.SerializeAllToConf(cdl)

	charonPod := &corev1.Pod{}
	err = r.Get(ctx, r.charonPodNsn(ipman.Spec.NodeName), charonPod)
	if apierrors.IsNotFound(err) {
		// wait for service to pick up webhooks
		for {
			charonPod = r.createCharonPod(ipman)
			if err := r.Create(ctx, charonPod); err != nil {
				if apierrors.IsInternalError(err) {
					logger.Info("Couldn't create charon pod", "error", err)
					time.Sleep(1 * time.Second)
					continue
				} else {
					logger.Error(err, "Fatal error trying to create charon pod")
					return nil, nil, err
				}
			}
			break
		}

	} else if err != nil {
		logger.Error(err, "Error checking Charon pod existence")
		return nil, nil, err
	}

	charonPod, err = waitForPodReady(charonPod, r.charonPodNsn(ipman.Spec.NodeName), r.Get)
	if err != nil {
		logger.Error(err, "Error while waiting for charon pod to be ready")
		return nil, nil, err
	}

	_, err = r.ensureRestctlPod(ctx, ipman)
	if err != nil {
		logger.Error(err, "Couldn't ensure restctl pod")
		return nil, nil, err
	}

	proxyPod, err := r.ensureCharonProxy(charonPod.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Couldn't ensure charon proxy pod")
		return nil, nil, err
	}
	proxyPod, err = waitForPodReady(proxyPod, r.proxyPodNsn(proxyPod.Name), r.Get)

	err = r.sendConfigToCharonPod(ctx, proxyPod, serializedConfig)
	if err != nil {
		return nil, nil, err
	}

	return charonPod, proxyPod, nil
}

func (r *IpmanReconciler) sendConfigToCharonPod(ctx context.Context, restctlPod *corev1.Pod, serializedConfig string) error {
	logger := log.FromContext(ctx)

	url := "http://" + restctlPod.Status.PodIP + "/reload"
	data := &comms.ReloadData{
		SerializedConfig: serializedConfig,
	}

	resp, err := comms.SendPost(url, data)
	for i := 0; err != nil && i < 3; i++ {
		logger.Error(err, "Couldn't send request to reload charon pod", "restctl-pod", restctlPod)
		time.Sleep(1 * time.Second)
		resp, err = comms.SendPost(url, data)
	}

	if resp.StatusCode != 200 {
		logger.Error(err, "error getting", "resp", resp)
		return fmt.Errorf("Error sending config to charon pod via restctl (%s)", restctlPod.Name)
	}

	return nil
}

func (r *IpmanReconciler) createCharonPod(ipman *ipmanv1.Ipman) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ipmanv1.CharonPodName + "-" + ipman.Spec.NodeName,
			Namespace: r.Env.NamespaceName,
			Labels: map[string]string{
				ipmanv1.CharonPodServiceAnnotation: "true", // to get picked up by the service
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": ipman.Spec.NodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{Name: ipmanv1.CharonSocketVolumeName},
				{Name: ipmanv1.CharonConfVolumeName},
				{Name: ipmanv1.CharonConnVolumeName},
				createCharonSocketVolume(r.Env.HostSocketsPath),
			},
			HostNetwork: true,
			HostPID:     true,
			Containers: []corev1.Container{
				r.createCharonDaemonContainer(),
			},
		},
	}
}

func (r *IpmanReconciler) ensureCharonProxy(nodeName string) (*corev1.Pod, error) {
	ctx := context.Background()
	logger := log.FromContext(ctx)

	proxyPodName := ipmanv1.CharonPodName + "-" + ipmanv1.CharonProxyPodSuffix
	proxyPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: proxyPodName, Namespace: r.Env.NamespaceName}, proxyPod)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Error fetching proxy pod")
			return nil, err
		}

		proxyPod = r.createCharonProxyPod(proxyPodName, nodeName)
		err = r.Create(ctx, proxyPod)
		if err != nil {
			logger.Error(err, "Error creating proxy pod", "spec", *proxyPod)
			return nil, err
		}
	}

	return proxyPod, nil
}

func (r *IpmanReconciler) proxyPodNsn(name string) types.NamespacedName {
	return types.NamespacedName{
		Name:      name,
		Namespace: r.Env.NamespaceName,
	}
}

func (r *IpmanReconciler) createCharonProxyPod(proxyPodName, nodeName string) *corev1.Pod {
	url := fmt.Sprintf("unix/%s%s", ipmanv1.CharonApiSocketVolumePath, "restctl.sock")
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyPodName,
			Namespace: r.Env.NamespaceName,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			Volumes: []corev1.Volume{
				createCharonSocketVolume(r.Env.HostSocketsPath),
			},
			Containers: []corev1.Container{
				{
					Name:            ipmanv1.CharonApiProxyContainerName,
					ImagePullPolicy: corev1.PullAlways,
					Image:           ipmanv1.CharonApiProxyContainerImage + ":" + ipmanv1.CharonApiProxyContainerImageTag,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "charon-host-socket",
							MountPath: ipmanv1.CharonApiSocketVolumePath,
						},
					},
					// it has to be --from :80 for it to be http on all interfaces
					Command:         []string{"caddy", "reverse-proxy", "--debug", "--from", ":80", "--to", url},
					SecurityContext: r.createNetAdminSecurityContext(),
				},
			},
		},
	}
}

func (r *IpmanReconciler) createCharonDaemonContainer() corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:            ipmanv1.CharonDaemonContainerName,
		Image:           r.Env.CharonDaemonImage,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "charon-host-socket", MountPath: ipmanv1.CharonSocketVolumeMountPath},
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
		},
		SecurityContext: r.createCharonDaemonSecurityContext(),
	}
}

func (r *IpmanReconciler) createRestCtlContainer() corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:            ipmanv1.CharonRestctlContainerName,
		Image:           r.Env.RestctlImage,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "charon-host-socket", MountPath: ipmanv1.CharonSocketVolumeMountPath},
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
			{Name: ipmanv1.CharonConnVolumeName, MountPath: ipmanv1.CharonConnVolumeMountPath},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "HOST_SOCKETS_PATH",
				Value: ipmanv1.CharonSocketVolumeMountPath,
			},
		},
		SecurityContext: r.createNetAdminSecurityContext(),
	}
}

func (r *IpmanReconciler) ensureRestctlPod(ctx context.Context, ipman *ipmanv1.Ipman) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)

	restctlPod := &corev1.Pod{}
	err := r.Get(ctx, r.restctlPodNsn(ipman.Spec.NodeName), restctlPod)
	if apierrors.IsNotFound(err) {
		for {
			restctlPod = r.createRestctlPod(ipman)
			if err := r.Create(ctx, restctlPod); err != nil {
				if apierrors.IsInternalError(err) {
					logger.Info("Couldn't create restctl pod", "error", err)
					time.Sleep(1 * time.Second)
					continue
				} else {
					logger.Error(err, "Fatal error trying to create restctl pod")
					return nil, err
				}
			}
			break
		}
	} else if err != nil {
		logger.Error(err, "Error checking restctl pod existence")
		return nil, err
	}

	restctlPod, err = waitForPodReady(restctlPod, r.restctlPodNsn(ipman.Spec.NodeName), r.Get)
	if err != nil {
		logger.Error(err, "Error while waiting for restctl pod to be ready")
		return nil, err
	}

	return restctlPod, nil
}

func (r *IpmanReconciler) restctlPodNsn(nodeName string) types.NamespacedName {
	ns := r.Env.NamespaceName
	name := "restctl-pod-" + nodeName
	return types.NamespacedName{
		Name:      name,
		Namespace: ns,
	}
}

func (r *IpmanReconciler) createRestctlPod(ipman *ipmanv1.Ipman) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Join([]string{ipmanv1.RestctlPodName, ipman.Spec.NodeName}, "-"),
			Namespace: r.Env.NamespaceName,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": ipman.Spec.NodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{Name: ipmanv1.CharonApiSocketVolumeName},
				{Name: ipmanv1.CharonSocketVolumeName},
				{Name: ipmanv1.CharonConfVolumeName},
				{Name: ipmanv1.CharonConnVolumeName},
				createCharonSocketVolume(r.Env.HostSocketsPath),
			},
			HostNetwork: true,
			HostPID:     true,
			Containers: []corev1.Container{
				r.createRestCtlContainer(),
			},
		},
	}
}

func createCharonSocketVolume(hostPath string) corev1.Volume {
	HostPathType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: "charon-host-socket",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: hostPath,
				Type: &HostPathType,
			},
		},
	}
}
