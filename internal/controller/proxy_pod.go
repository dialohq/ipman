package controller

import (
	"context"
	"fmt"
	"io"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ProxyPodSpec defines the specification for a Proxy pod
type ProxyPodSpec struct {
	HostPath string `json:"host_path" diff:"host_path"`
	// NodeName string `json:"node"`
}

// ApplySpec implements the IpmanPodSpec interface for ProxyPodSpec
func (s ProxyPodSpec) ApplySpec(p *corev1.Pod, e Envs) {
	url := fmt.Sprintf("unix/%s%s", ipmanv1.CharonAPISocketVolumePath, "restctl.sock")

	p.Spec.Volumes = []corev1.Volume{
		createCharonSocketVolume(e.HostSocketsPath),
	}
	p.Spec.Containers = []corev1.Container{
		{
			Name:            ipmanv1.CharonAPIProxyContainerName,
			ImagePullPolicy: corev1.PullPolicy(e.CaddyProxyPullPolicy),
			Image:           e.CaddyImage,
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      ipmanv1.CharonSocketHostVolumeName,
					MountPath: ipmanv1.CharonAPISocketVolumePath,
				},
			},
			Command: []string{"caddy", "reverse-proxy", "--from", ":80", "--to", url},
		},
	}
}

func groupConnsByNode(list []ipmanv1.IPSecConnection) map[string][]ipmanv1.IPSecConnection {
	ipmen := map[string][]ipmanv1.IPSecConnection{}
	for _, im := range list {
		if _, ok := ipmen[im.Spec.NodeName]; !ok {
			ipmen[im.Spec.NodeName] = []ipmanv1.IPSecConnection{}
		}
		ipmen[im.Spec.NodeName] = append(ipmen[im.Spec.NodeName], im)
	}
	return ipmen
}

func (s ProxyPodSpec) CompleteSetup(r *IPSecConnectionReconciler, pod *corev1.Pod, node string) error {
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
	serializedConf := ipmanv1.SerializeAllToConf(cdl)
	url := "http://" + pod.Status.PodIP + "/reload"
	data := &comms.ReloadData{
		SerializedConfig: serializedConf,
	}
	resp, err := comms.SendPost(url, data)
	for i := 0; err != nil && i < 3; i++ {
		time.Sleep(time.Second / 2)
		resp, err = comms.SendPost(url, data)
	}
	if err != nil {
		return fmt.Errorf("Couldn't send request to reload charon pod: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("Error reloading charon pod, status code not 200: %d, %s", resp.StatusCode, string(out))
	}

	byNode := groupConnsByNode(list.Items)
	conns, ok := byNode[node]
	if !ok {
		return fmt.Errorf("Node '%s' not found", node)
	}
	for _, c := range conns {
		c.Status.CharonProxyIP = pod.Status.PodIP
		err = r.Status().Update(ctx, &c)
		if err != nil {
			return fmt.Errorf("Couldn't add proxy pod ip to status")
		}
	}
	return nil
}
func (s ProxyPodSpec) CompleteDeletion(r *IPSecConnectionReconciler, pod *corev1.Pod, node string) error {
	ctx := context.Background()
	// TODO: maybe should add another CR for global state?
	list := ipmanv1.IPSecConnectionList{}
	err := r.List(ctx, &list)
	if err != nil {
		return fmt.Errorf("Couldn't fetch list of connections to complete deletion")
	}
	byNode := groupConnsByNode(list.Items)
	conns, ok := byNode[node]
	if !ok {
		return fmt.Errorf("Node '%s' not found", node)
	}
	for _, c := range conns {
		c.Status.CharonProxyIP = ""
		err := r.Status().Update(ctx, &c)
		if err != nil {
			return fmt.Errorf("Couldn't remove charon proxy ip from status: %w", err)
		}
	}
	return nil
}
