package controller

import (
	"context"
	"fmt"
	"os"
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

func charonPodNsn(ipmanName string) types.NamespacedName {
	ns := ipmanv1.IpmanSystemNamespace
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
		cdl = append(cdl, ipmanv1.ConnData{Secret: string(sec.Data[ipmanv1.SecretKey]), Ipman: im})
	}

	serializedConfig := ipman.Spec.SerializeAllToConf(cdl)

	charonPod := &corev1.Pod{}
	err = r.Get(ctx, charonPodNsn(ipman.Spec.NodeName), charonPod)
	if apierrors.IsNotFound(err) {
		// wait for service to pick up webhooks
		for {
			charonPod = r.createCharonPod(serializedConfig, ipman)
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

	charonPod, err = waitForPodReady(charonPod, charonPodNsn(ipman.Spec.NodeName), r.Get)
	if err != nil {
		logger.Error(err, "Error while waiting for charon pod to be ready")
		return nil, nil, err
	}

	proxyPod, err := r.ensureCharonProxy(charonPod.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Couldn't ensure charon proxy pod")
		return nil, nil, err
	}
	proxyPod, err = waitForPodReady(proxyPod, proxyPodNsn(proxyPod.Name), r.Get)

	url := "http://" + proxyPod.Status.PodIP + "/reload"
	data := &comms.ReloadData{
		SerializedConfig: serializedConfig,
	}

	resp, err := comms.SendPost(url, data)
	for i := 0; err != nil && i < 3; i++ {
		logger.Error(err, "Couldn't send request to reload charon pod", "charon-pod", charonPod)
		time.Sleep(1 * time.Second)
		resp, err = comms.SendPost(url, data)
	}

	if resp.StatusCode != 200 {
		logger.Error(err, "error getting", "resp", resp)
		return nil, nil, fmt.Errorf("Error reloading charon pod (%s)", charonPod.Name)
	}

	return charonPod, proxyPod, nil
}

func (r *IpmanReconciler) createCharonPod(serializedConfig string, ipman *ipmanv1.Ipman) *corev1.Pod {
	path := os.Getenv(ipmanv1.ProxySocketPathEnvVarName)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ipmanv1.CharonPodName + "-" + ipman.Spec.NodeName,
			Namespace: ipmanv1.IpmanSystemNamespace,
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
				createCharonProxySocketVolume(path),
			},
			HostNetwork: true,
			HostPID:     true,
			Containers: []corev1.Container{
				r.createCharonDaemonContainer(),
				r.createRestCtlContainer(),
			},
			InitContainers: []corev1.Container{
				r.createConfInitContainer(serializedConfig),
			},
		},
	}
}

func (r *IpmanReconciler) ensureCharonProxy(nodeName string) (*corev1.Pod, error) {
	ctx := context.Background()
	logger := log.FromContext(ctx)

	proxyPodName := ipmanv1.CharonPodName + "-" + ipmanv1.CharonProxyPodSuffix
	proxyPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: proxyPodName, Namespace: ipmanv1.IpmanSystemNamespace}, proxyPod)
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

func createCharonProxySocketVolume(path string) corev1.Volume {
	HostPathType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: ipmanv1.CharonApiSocketVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: path,
				Type: &HostPathType,
			},
		},
	}
}

func proxyPodNsn(name string) types.NamespacedName {
	return types.NamespacedName{
		Name:      name,
		Namespace: ipmanv1.IpmanSystemNamespace,
	}
}

func (r *IpmanReconciler) createCharonProxyPod(proxyPodName, nodeName string) *corev1.Pod {
	url := fmt.Sprintf("unix/%s%s", ipmanv1.CharonApiSocketVolumePath, "restctl.sock")
	path := ipmanv1.CharonApiProxySocketHostPath
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyPodName,
			Namespace: ipmanv1.IpmanSystemNamespace,
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			Volumes: []corev1.Volume{
				createCharonProxySocketVolume(path),
			},
			Containers: []corev1.Container{
				{
					Name:            ipmanv1.CharonApiProxyContainerName,
					ImagePullPolicy: corev1.PullAlways,
					Image:           ipmanv1.CharonApiProxyContainerImage + ":" + ipmanv1.CharonApiProxyContainerImageTag,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      ipmanv1.CharonApiSocketVolumeName,
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
		Image:           ipmanv1.CharonDaemonContainerImage + ":" + ipmanv1.CharonDaemonContainerImageTag,
		VolumeMounts: []corev1.VolumeMount{
			{Name: ipmanv1.CharonSocketVolumeName, MountPath: ipmanv1.CharonSocketVolumeMountPath},
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
		},
		SecurityContext: r.createCharonDaemonSecurityContext(),
	}
}

func (r *IpmanReconciler) createRestCtlContainer() corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:            ipmanv1.CharonRestctlContainerName,
		Image:           ipmanv1.CharonRestctlImage + ":" + ipmanv1.CharonRestctlImageTag,
		VolumeMounts: []corev1.VolumeMount{
			{Name: ipmanv1.CharonApiSocketVolumeName, MountPath: ipmanv1.CharonApiSocketVolumePath},
			{Name: ipmanv1.CharonSocketVolumeName, MountPath: ipmanv1.CharonSocketVolumeMountPath},
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
			{Name: ipmanv1.CharonConnVolumeName, MountPath: ipmanv1.CharonConnVolumeMountPath},
		},
		SecurityContext: r.createNetAdminSecurityContext(),
	}
}

func (r *IpmanReconciler) createConfInitContainer(serializedConfig string) corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:            ipmanv1.CharonCreateConfContainerName,
		Image:           ipmanv1.CharonCreateConfImage + ":" + ipmanv1.CharonCreateConfImageTag,
		Command: []string{
			"sh", "-c",
			fmt.Sprintf("echo '%s' > %s/swanctl.conf && chmod 666 %s/swanctl.conf",
				serializedConfig, ipmanv1.CharonConfVolumeMountPath, ipmanv1.CharonConfVolumeMountPath),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
		},
		SecurityContext: r.createDefaultSecurityContext(),
	}
}
