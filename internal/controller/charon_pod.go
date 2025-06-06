package controller

import (
	ipmanv1 "dialo.ai/ipman/api/v1"
	corev1 "k8s.io/api/core/v1"
)

// CharonPodSpec defines the specification for a Charon pod with restctl container
type CharonPodSpec struct {
	HostPath string `json:"host_path" diff:"host_path"`
}

// ApplySpec implements the IpmanPodSpec interface for CharonPodSpec
func (s CharonPodSpec) ApplySpec(p *corev1.Pod, e Envs) {
	p.Spec.Volumes = []corev1.Volume{
		createCharonSocketVolume(s.HostPath),
		{
			Name: ipmanv1.CharonConfVolumeName,
		},
	}
	p.Spec.Containers = []corev1.Container{
		{
			Name:            ipmanv1.CharonDaemonContainerName,
			Image:           e.CharonDaemonImage,
			ImagePullPolicy: corev1.PullPolicy(e.CharonDaemonPullPolicy),
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      ipmanv1.CharonSocketHostVolumeName,
					MountPath: ipmanv1.CharonSocketVolumeMountPath,
				},
				{
					Name:      ipmanv1.CharonConfVolumeName,
					MountPath: ipmanv1.CharonConfVolumeMountPath,
				},
			},
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{
						"NET_ADMIN",
						"NET_RAW",
					},
				},
			},
		},
		{
			Name:            ipmanv1.CharonRestctlContainerName,
			Image:           e.RestctlImage,
			ImagePullPolicy: corev1.PullPolicy(e.RestctlPullPolicy),
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      ipmanv1.CharonSocketHostVolumeName,
					MountPath: ipmanv1.CharonSocketVolumeMountPath,
				},
				{
					Name:      ipmanv1.CharonConfVolumeName,
					MountPath: ipmanv1.CharonConfVolumeMountPath,
				},
			},
			Env: []corev1.EnvVar{
				{
					Name:  "HOST_SOCKETS_PATH",
					Value: ipmanv1.CharonSocketVolumeMountPath,
				},
			},
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{
						"NET_ADMIN",
					},
				},
			},
		},
	}
	p.Spec.HostNetwork = true
	p.Spec.HostPID = true
	p.Spec.RestartPolicy = corev1.RestartPolicyNever
}

func (s CharonPodSpec) CompleteSetup(r *IPSecConnectionReconciler, pod *corev1.Pod, node string) error {
	return nil
}
func (s CharonPodSpec) CompleteDeletion(r *IPSecConnectionReconciler, pod *corev1.Pod, node string) error {
	return nil
}

func createCharonSocketVolume(hostPath string) corev1.Volume {
	HostPathType := corev1.HostPathDirectoryOrCreate
	return corev1.Volume{
		Name: ipmanv1.CharonSocketHostVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: hostPath,
				Type: &HostPathType,
			},
		},
	}
}
