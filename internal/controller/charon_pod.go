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

	charonPod, err = waitForPodReady(charonPod, r.charonPodNsn(ipman.Spec.NodeName), r.Get)
	if err != nil {
		logger.Error(err, "Error while waiting for charon pod to be ready")
		return nil, nil, err
	}


	restctlPod, err := r.ensureRestctlPod(ctx, charonPod.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Couldn't ensure restctl pod")
		return nil, nil, err
	}
	restctlPod, err = waitForPodReady(restctlPod, r.restctlPodNsn(restctlPod.Name), r.Get)
	if err != nil {
		logger.Error(err, "Error while waiting for restctl pod to be ready")
		return nil, nil, err
	}

	url := "http://" + restctlPod.Status.PodIP + "/reload"
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

	return charonPod, restctlPod, nil
}

func (r *IpmanReconciler) ensureRestctlPod(ctx context.Context, nodeName string) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)

	restctlPodName := ipmanv1.CharonPodName + "-restctl"
	restctlPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{Name: restctlPodName, Namespace: r.Env.NamespaceName}, restctlPod)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Error fetching restctl pod")
			return nil, err
		}

		restctlPod = r.createRestctlPod(restctlPodName, nodeName)
		err = r.Create(ctx, restctlPod)
		if err != nil {
			logger.Error(err, "Error creating restctl pod", "spec", *restctlPod)
			return nil, err
		}
	}

	return restctlPod, nil
}

func (r *IpmanReconciler) restctlPodNsn(name string) types.NamespacedName {
	return types.NamespacedName{
		Name:      name,
		Namespace: r.Env.NamespaceName,
	}
}

func (r *IpmanReconciler) createRestctlPod(restctlPodName, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restctlPodName,
			Namespace: r.Env.NamespaceName,
			Labels: map[string]string{
				ipmanv1.CharonPodServiceAnnotation: "true", // to get picked up by the service
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			Volumes: []corev1.Volume{
				createCharonSocketVolume(),
				{Name: ipmanv1.CharonConfVolumeName},
				{Name: ipmanv1.CharonConnVolumeName},
			},
			Containers: []corev1.Container{
				r.createRestCtlContainerWithHTTP(),
			},
		},
	}
}

func (r *IpmanReconciler) createCharonPod(serializedConfig string, ipman *ipmanv1.Ipman) *corev1.Pod {
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
				createCharonSocketVolume(),
				{Name: ipmanv1.CharonConfVolumeName},
				{Name: ipmanv1.CharonConnVolumeName},
			},
			HostNetwork: true,
			HostPID:     true,
			Containers: []corev1.Container{
				r.createCharonDaemonContainer(),
			},
			InitContainers: []corev1.Container{
				r.createConfInitContainer(serializedConfig),
			},
		},
	}
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

func createCharonSocketVolume() corev1.Volume {
	HostPathType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: ipmanv1.CharonSocketVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: ipmanv1.CharonSocketVolumeMountPath,
				Type: &HostPathType,
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
		Image:           r.Env.RestctlImage,
		VolumeMounts: []corev1.VolumeMount{
			{Name: ipmanv1.CharonApiSocketVolumeName, MountPath: ipmanv1.CharonApiSocketVolumePath},
			{Name: ipmanv1.CharonSocketVolumeName, MountPath: ipmanv1.CharonSocketVolumeMountPath},
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
			{Name: ipmanv1.CharonConnVolumeName, MountPath: ipmanv1.CharonConnVolumeMountPath},
		},
		SecurityContext: r.createNetAdminSecurityContext(),
	}
}

func (r *IpmanReconciler) createRestCtlContainerWithHTTP() corev1.Container {
	return corev1.Container{
		ImagePullPolicy: corev1.PullAlways,
		Name:            ipmanv1.CharonRestctlContainerName,
		Image:           r.Env.RestctlImage,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 80,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
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
			fmt.Sprintf("echo '%s' > %sswanctl.conf && chmod 666 %sswanctl.conf && touch /etc/strongswan.conf",
				serializedConfig, ipmanv1.CharonConfVolumeMountPath, ipmanv1.CharonConfVolumeMountPath),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: ipmanv1.CharonConfVolumeName, MountPath: ipmanv1.CharonConfVolumeMountPath},
		},
		SecurityContext: r.createDefaultSecurityContext(),
	}
}
