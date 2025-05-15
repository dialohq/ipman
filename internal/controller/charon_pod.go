package controller

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"dialo.ai/ipman/pkg/comms"
	ipmanv1 "dialo.ai/ipman/api/v1"
	"k8s.io/apimachinery/pkg/types"
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

func (r *IpmanReconciler) ensureCharonPod(ctx context.Context, secret *corev1.Secret, ipman *ipmanv1.Ipman) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)

	charonPod := &corev1.Pod{}
	err := r.Get(ctx, charonPodNsn(ipman.Name), charonPod)
	if apierrors.IsNotFound(err) {
		// charon pod doesn't exist

		charonPod = r.createCharonPod(secret, ipman)
		if err := r.Create(ctx, charonPod); err != nil {
			logger.Error(err, "Failed to create Charon pod")
			return nil, err
		}

	} else if err != nil {
		logger.Error(err, "Error checking Charon pod existence")
		return nil, err
	}

	charonPod, err = waitForPodReady(charonPod, charonPodNsn(ipman.Name), r.Get)
	if err != nil {
		logger.Error(err, "Error while waiting for charon pod to be ready")
		return nil, err
	}

	url := "http://" + charonPod.Status.PodIP + ":8080/reload"
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
		return nil, fmt.Errorf("Error reloading charon pod (%s)", charonPod.Name)
	}

	return charonPod, nil
}

func (r *IpmanReconciler) createCharonPod(secret *corev1.Secret, ipman *ipmanv1.Ipman) *corev1.Pod {
	vs := corev1.VolumeSource{
		EmptyDir: &corev1.EmptyDirVolumeSource{},
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CharonPodName + "-" + ipman.Name,
			Namespace: "ims",
			Labels: map[string]string{
				"ipserviced": "true", // to get picked up by the service
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": ipman.Spec.NodeName,
			},
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
			fmt.Sprintf("echo '%s' > /etc/swanctl/swanctl.conf && chmod 666 /etc/swanctl/swanctl.conf",
				ipman.Spec.SerializeToConf(string(secret.Data["psk"]))),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: CharonConfVolume, MountPath: "/etc/swanctl"},
		},
		SecurityContext: r.createDefaultSecurityContext(),
	}
}
