package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *IPSecConnectionReconciler) xfrmPodNsn(childName, ipsecconnectionName string) types.NamespacedName {
	ns := r.Env.NamespaceName
	podNameEnv := ipmanv1.XfrmPodName
	podName := strings.Join([]string{podNameEnv, childName, ipsecconnectionName}, "-")

	return types.NamespacedName{
		Namespace: ns,
		Name:      podName,
	}
}

func (r *IPSecConnectionReconciler) ensureXfrmPod(ipsecconnection *ipmanv1.IPSecConnection, ctx context.Context, c *ipmanv1.Child) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)

	xfrmPod := &corev1.Pod{}

	err := r.Get(ctx, r.xfrmPodNsn(c.Name, ipsecconnection.Name), xfrmPod)
	xfrmUrl := ""
	if apierrors.IsNotFound(err) {

		xfrmPod = r.createXfrmPod(c, ipsecconnection)
		if err := r.Create(ctx, xfrmPod); err != nil {
			logger.Error(err, "Failed to create xfrm pod")
			return nil, err
		}

		xfrmPod, err = waitForPodReady(xfrmPod, r.xfrmPodNsn(c.Name, ipsecconnection.Spec.Name), r.Get)
		if err != nil {
			logger.Error(err, "Error waiting for xfrm pod to be ready", "pod", xfrmPod.Name)
			return nil, err
		}

		resp, err := http.Get(fmt.Sprintf("http://%s:8080/pid", xfrmPod.Status.PodIP))
		if err != nil {
			logger.Error(err, "Couldn't request PID from xfrm pod", ipmanv1.XfrmPodName, xfrmPod.Name)
			return nil, err
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Error(err, "Couldn't read body of pid response", ipmanv1.XfrmPodName, xfrmPod.Name)
			return nil, err
		}

		prd := &comms.PidResponseData{}
		err = json.Unmarshal(out, prd)
		if err != nil {
			logger.Error(err, "Couldn't unmarshal body of pid response", ipmanv1.XfrmPodName, xfrmPod.Name)
			return nil, err
		}

		if prd.Error != "" {
			err = fmt.Errorf("%s", prd.Error)
			logger.Error(err, "Error in PID response from xfrm pod")
			return nil, err
		}

		xfrmRequest := comms.XfrmRequestData{
			XfrmIfId: c.XfrmIfId,
			PID:      prd.Pid,
		}

		charonUrl := fmt.Sprintf("http://%s", ipsecconnection.Status.CharonProxyIP)
		resp, err = comms.SendPost(charonUrl+"/xfrm", xfrmRequest)

		if err != nil {
			logger.Error(err, "Couldn't send request for xfrm interface")
			return nil, err
		}

		defer resp.Body.Close()
		out, err = io.ReadAll(resp.Body)
		if err != nil {
			logger.Error(err, "Couldn't read body of request for xfrm interface")
			return nil, err
		}

		rd := &comms.XfrmResponseData{}
		err = json.Unmarshal(out, rd)
		if err != nil {
			logger.Error(err, "Couldn't unmarshal body of response to request for xfrm interface", "body", string(out))
			return nil, err
		}
		if rd.Error != "" {
			err = fmt.Errorf("%s", rd.Error)
			logger.Error(err, "Error moving interface to xfrm pod in charon-pod")
			return nil, err
		}

		xfrmUrl = fmt.Sprintf("http://%s:8080", xfrmPod.Status.PodIP)
		resp, err = comms.SendPost(xfrmUrl+"/addRoutes", c)
		if err != nil {
			logger.Error(err, "Error sending post to /addRoutes in xfrm pod", "child", c, "pod", xfrmPod)
			return nil, err
		}
		if resp.StatusCode != 200 {
			err = errors.New("status code not 200")
			logger.Error(err, "Error requesting /addRoutes in xfrm pod", "status", resp.StatusCode, "pod", xfrmPod)
			return nil, err
		}

		return xfrmPod, nil
	} else if err != nil {
		logger.Error(err, "Error checking xfrm pod existence")
		return nil, err
	}

	return xfrmPod, nil

}

func (r *IPSecConnectionReconciler) createXfrmPod(c *ipmanv1.Child, ipsecconnection *ipmanv1.IPSecConnection) *corev1.Pod {
	remoteIpsJSON, _ := json.Marshal(c.RemoteIps)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.Join([]string{ipmanv1.XfrmPodName, c.Name, ipsecconnection.Spec.Name}, "-"),
			Namespace: r.Env.NamespaceName,
			Labels: map[string]string{
				ipmanv1.XfrmPodLabelKey: ipsecconnection.Spec.Name,
			},
			OwnerReferences: []metav1.OwnerReference{createOwnerReference(ipsecconnection)},
			Annotations: map[string]string{
				ipmanv1.AnnotationIpmanName:  ipsecconnection.Spec.Name,
				ipmanv1.AnnotationChildName:  c.Name,
				ipmanv1.AnnotationVxlanIp:    c.VxlanIP,
				ipmanv1.AnnotationXfrmIp:     c.XfrmIP,
				ipmanv1.AnnotationRemoteIps:  string(remoteIpsJSON),
				ipmanv1.AnnotationIntefaceId: strconv.FormatInt(int64(c.XfrmIfId), 10),
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": ipsecconnection.Spec.NodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			HostPID:       true,
			SecurityContext: &corev1.PodSecurityContext{
				Sysctls: []corev1.Sysctl{
					{
						Name:  "net.ipv4.ip_forward",
						Value: "1",
					},
					{
						Name:  "net.ipv4.conf.all.arp_filter",
						Value: "1",
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:            ipmanv1.XfrminionContainerName,
					Image:           r.Env.XfrminionImage,
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: r.createNetAdminSecurityContext(),
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/healthz",
								Port: intstr.FromInt(8080),
							},
						},
					},
				},
			},
		},
	}
}
