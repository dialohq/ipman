package controller

import (
	"context"
	"fmt"

	ipmanv1 "dialo.ai/ipman/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// RestctlPodSpec defines the specification for a Restctl pod
type RestctlPodSpec struct {
	HostPath string `json:"host_path" diff:"host_path"`
}

// ApplySpec implements the IpmanPodSpec interface for RestctlPodSpec
func (s RestctlPodSpec) ApplySpec(p *corev1.Pod, e Envs) {
	p.Spec.Volumes = []corev1.Volume{
		createCharonSocketVolume(s.HostPath),
	}
	p.Spec.HostPID = true
	p.Spec.Containers = []corev1.Container{
		{
			Name:            ipmanv1.RestctlContainerName,
			Image:           e.RestctlImage,
			ImagePullPolicy: corev1.PullPolicy(e.RestctlPullPolicy),
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      ipmanv1.CharonSocketHostVolumeName,
					MountPath: ipmanv1.CharonSocketVolumeMountPath,
				},
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          ipmanv1.RestctlPortName,
					ContainerPort: ipmanv1.RestctlPort,
					Protocol:      corev1.ProtocolTCP,
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
						"NET_RAW",
					},
				},
			},
		},
	}
}

func groupConnsByGroup(list []ipmanv1.IPSecConnection) map[types.NamespacedName][]ipmanv1.IPSecConnection {
	conns := map[types.NamespacedName][]ipmanv1.IPSecConnection{}
	for _, c := range list {
		if _, ok := conns[c.Spec.Group.Nsn()]; !ok {
			conns[c.Spec.Group.Nsn()] = []ipmanv1.IPSecConnection{}
		}
		conns[c.Spec.Group.Nsn()] = append(conns[c.Spec.Group.Nsn()], c)
	}
	return conns
}

func (s RestctlPodSpec) CompleteSetup(r *IPSecConnectionReconciler, pod *corev1.Pod, groupNsn types.NamespacedName) error {
	ctx := context.Background()
	list := &ipmanv1.IPSecConnectionList{}
	err := r.List(ctx, list)
	if err != nil {
		return fmt.Errorf("Failed to liset ipsec connections:, %w", err)
	}
	cdl := []ipmanv1.ConnData{}
	for _, ipsecconn := range list.Items {
		sec := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{
			Name:      ipsecconn.Spec.SecretRef.Name,
			Namespace: ipsecconn.Spec.SecretRef.Namespace}, sec)
		if err != nil {
			return fmt.Errorf("Failed to find secret for connection %s: %w", ipsecconn.Name, err)
		}
		cdl = append(cdl, ipmanv1.ConnData{Secret: string(sec.Data[ipsecconn.Spec.SecretRef.Key]), IPSecConnection: ipsecconn})
	}
	pod, err = r.waitForPodReady(types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
	if err != nil {
		return err
	}
	return nil
}
func (s RestctlPodSpec) CompleteDeletion(r *IPSecConnectionReconciler, pod *corev1.Pod, groupNsn types.NamespacedName) error {
	return nil
}
