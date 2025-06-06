package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// XfrmPodSpec defines the specification for an Xfrm pod
type XfrmPodSpec struct {
	Routes Routes         `json:"routes" diff:"routes"`
	Props  XfrmProperties `json:"properties" diff:"props"`
}

// ApplySpec implements the IpmanPodSpec interface for XfrmPodSpec
func (s XfrmPodSpec) ApplySpec(p *corev1.Pod, e Envs) {
	p.Spec.Containers = []corev1.Container{
		{
			Name:            ipmanv1.XfrminionContainerName,
			Image:           e.XfrminionImage,
			ImagePullPolicy: corev1.PullPolicy(e.XfrminionPullPolicy),
			SecurityContext: &corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{
						"NET_ADMIN",
					},
				},
			},
		},
	}
	p.Spec.RestartPolicy = corev1.RestartPolicyNever
	p.Spec.HostPID = true
}

func (s XfrmPodSpec) CompleteSetup(r *IPSecConnectionReconciler, pod *corev1.Pod, node string) error {
	ctx := context.Background()
	nsn := types.NamespacedName{Name: s.Props.OwnerConnection, Namespace: ""}
	isc := &ipmanv1.IPSecConnection{}
	err := r.Get(ctx, nsn, isc)
	if err != nil {
		return fmt.Errorf("Couldn't get ipsec connection for pod '%s': %w", pod.Name, err)
	}

	for pod.Status.PodIP == "" {
		pod, err = r.waitForPodReady(types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace})
		if err != nil {
			return err
		}
	}
	url := fmt.Sprintf("http://%s:8080", pod.Status.PodIP)
	resp, err := http.Get(url + "/pid")
	if err != nil {
		return fmt.Errorf("Couldn't get xfrm pods '%s' PID: %w", pod.Name, err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Couldn't get xfrm pods '%s' PID: Status code not 200, is %d", pod.Name, resp.StatusCode)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Couldn't read body of response for xfrm '%s' PID: %w", pod.Name, err)
	}
	prd := &comms.PidResponseData{}
	err = json.Unmarshal(out, prd)
	if err != nil {
		return fmt.Errorf("Couldn't unmarshal response for xfrm '%s' PID: %w", pod.Name, err)
	}

	xfrmRequest := &comms.XfrmRequestData{
		XfrmIfId: int(s.Props.InterfaceID),
		PID:      prd.Pid,
	}
	charonUrl := fmt.Sprintf("http://%s/xfrm", isc.Status.CharonProxyIP)
	resp, err = comms.SendPost(charonUrl, xfrmRequest)
	if err != nil {
		return fmt.Errorf("Couldn't create xfrm interface for pod '%s':  %w", pod.Name, err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Couldn't create xfrm interface for pod '%s' PID: Status code not 200, is %d (IP: %s)", pod.Name, resp.StatusCode, isc.Status.CharonProxyIP)
	}

	resp, err = comms.SendPost(url+"/setupVxlan", comms.SetupVxlanRequest{
		XfrmIP:  s.Props.XfrmIP,
		VxlanIP: s.Props.VxlanIP,
		XfrmID:  int(s.Props.InterfaceID),
	})
	if err != nil {
		return fmt.Errorf("Couldn't create vxlan interface for pod '%s':  %w", pod.Name, err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Couldn't create vxlan interface for pod '%s' PID: Status code not 200, is %d (IP: %s)", pod.Name, resp.StatusCode, isc.Status.CharonProxyIP)
	}

	if isc.Status.XfrmGatewayIPs == nil {
		isc.Status.XfrmGatewayIPs = map[string]string{}
	}
	isc.Status.XfrmGatewayIPs[s.Props.OwnerChild] = pod.Status.PodIP
	r.Status().Update(ctx, isc)
	return nil
}
func (s XfrmPodSpec) CompleteDeletion(r *IPSecConnectionReconciler, pod *corev1.Pod, node string) error {
	ctx := context.Background()
	nsn := types.NamespacedName{Name: s.Props.OwnerConnection, Namespace: ""}
	isc := &ipmanv1.IPSecConnection{}
	err := r.Get(ctx, nsn, isc)
	if err != nil {
		return fmt.Errorf("Couldn't get ipsec connection for pod '%s': %w", pod.Name, err)
	}
	delete(isc.Status.XfrmGatewayIPs, s.Props.OwnerChild)
	r.Status().Update(ctx, isc)
	return nil
}

// XfrmProperties holds the configuration properties for an Xfrm pod
type XfrmProperties struct {
	OwnerChild      string `json:"owner_child" diff:"owner_child"`
	OwnerConnection string `json:"owner_connection" diff:"owner_connection"`
	InterfaceID     uint32 `json:"interface_id" diff:"interface_id"`
	XfrmIP          string `json:"xfrm_ip" diff:"xfrm_ip"`
	VxlanIP         string `json:"vxlan_ip" diff:"vxlan_ip"`
}
