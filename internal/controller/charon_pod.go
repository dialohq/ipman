package controller

import (
	"context"
	"fmt"
	"os"
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
	// TODO
	// ns := os.Getenv("NAMESPACE_NAME")
	// nameEnv := os.Getenv("CHARON_POD_NAME")
	ns := "ims"
	nameEnv := "charon-pod"

	name := fmt.Sprintf("%s-%s", nameEnv, ipmanName)
	return types.NamespacedName {
		Name: name,
		Namespace: ns,
	}
}

func (r *IpmanReconciler) ensureCharonPod(ctx context.Context, secret *corev1.Secret, ipman *ipmanv1.Ipman) (*corev1.Pod, *corev1.Pod, error) {
	logger := log.FromContext(ctx)

	charonPod := &corev1.Pod{}
	err := r.Get(ctx, charonPodNsn(ipman.Spec.NodeName), charonPod)
	if apierrors.IsNotFound(err) {
		// charon pod doesn't exist

		// wait for service to pick up webhooks
		for {
			charonPod = r.createCharonPod(secret, ipman)
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

	proxyPod, err := r.ensureCharonProxy(charonPod.Name, charonPod.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Couldn't ensure charon proxy pod")
		return nil, nil, err
	}
	proxyPod, err = waitForPodReady(proxyPod, proxyPodNsn(proxyPod.Name), r.Get)

	url := "http://" + proxyPod.Status.PodIP + "/reload"
	data := &comms.ReloadData{
		SerializedConfig: ipman.Spec.SerializeToConf(string(secret.Data["psk"])),
	}
	resp, err := comms.SendPost(url, data)
	for i := 0; err != nil && i < 3; i++ {
		logger.Error(err, "Couldn't send request to reload charon pod", "charon-pod", charonPod)
		time.Sleep(1 * time.Second)
		resp, err = comms.SendPost(url, data)
	}

	if resp.StatusCode != 200{
		logger.Error(err, "error getting", "resp", resp)
		return nil, nil, fmt.Errorf("Error reloading charon pod (%s)", charonPod.Name)
	}

	return charonPod, proxyPod, nil
}

func (r *IpmanReconciler) createCharonPod(secret *corev1.Secret, ipman *ipmanv1.Ipman) *corev1.Pod {
	path := os.Getenv("PROXY_SOCKET_PATH")
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CharonPodName + "-" + ipman.Spec.NodeName,
			Namespace: "ims",
			Labels: map[string]string{
				"ipserviced": "true", // to get picked up by the service
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": ipman.Spec.NodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{Name: CharonSocketVolume},
				{Name: CharonConfVolume},
				{Name: "charon-conn"},
				createCharonProxySocketVolume(path),
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

func (r *IpmanReconciler) ensureCharonProxy(charonPodName, nodeName string) (*corev1.Pod, error) {
	ctx := context.Background()
	logger := log.FromContext(ctx)

	proxyPodName := charonPodName+"-proxy"
	proxyPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: proxyPodName, Namespace: "ims"}, proxyPod)
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

func createCharonProxySocketVolume(path string) corev1.Volume{
	HostPathType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: CharonApiSocketVolumeName,
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
		Name:name,
		Namespace: "ims",
	}
}

func (r *IpmanReconciler) createCharonProxyPod(proxyPodName, nodeName string) *corev1.Pod {
	url := fmt.Sprintf("unix/%s%s", CharonApiSocketVolumePath, "restctl.sock")
	path := os.Getenv("PROXY_SOCKET_PATH")
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: proxyPodName,
			Namespace: "ims",
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
					Name: "caddy-proxy",
					ImagePullPolicy: corev1.PullAlways,
					Image: "caddy:2.10.0-alpine",
					VolumeMounts: []corev1.VolumeMount {
						{
							Name: CharonApiSocketVolumeName,
							MountPath: CharonApiSocketVolumePath,
						},
					},
					// it has to be --from :80 for it to be http on all interfaces
					Command: []string{"caddy", "reverse-proxy", "--debug", "--from", ":80",  "--to", url},
					SecurityContext: r.createNetAdminSecurityContext(),
				},
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
			{Name: CharonApiSocketVolumeName, MountPath: CharonApiSocketVolumePath},
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
			fmt.Sprintf("echo '%s' > /etc/swanctl/swanctl.conf && chmod 666 /etc/swanctl/swanctl.conf",
				ipman.Spec.SerializeToConf(string(secret.Data["psk"]))),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
		},
		SecurityContext: r.createDefaultSecurityContext(),
	}
}
