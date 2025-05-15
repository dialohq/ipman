package controller

import (
	"strings"
	"fmt"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"strconv"
	"errors"
	"encoding/json"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	"context"
	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func xfrmPodNsn(childName string) types.NamespacedName {
	// podNameEnv := os.Getenv("XFRM_POD_NAME")
	// ns := os.Getenv("NAMESPACE_NAME")
	ns := "ims"
	podNameEnv := "xfrm-pod"
	podName := strings.Join([]string{podNameEnv, childName}, "-")

	return types.NamespacedName{
		Namespace: ns,
		Name: podName,
	}
}

func (r *IpmanReconciler) ensureXfrmPod(ctx context.Context, c *ipmanv1.Child, nodeName string, charonPodIp string) (*corev1.Pod, error){
	logger := log.FromContext(ctx)

	xfrmPod := &corev1.Pod{}
	nsn := types.NamespacedName{
		Name:      XfrmPodName + "-" + c.Name,
		Namespace: "ims",
	}

	err := r.Get(ctx, nsn, xfrmPod)
	if apierrors.IsNotFound(err) {

		xfrmPod = r.createXfrmPod(c, nodeName)
		if err := r.Create(ctx, xfrmPod); err != nil {
			logger.Error(err, "Failed to create xfrm pod")
			return nil, err
		}
		
		xfrmPod, err = waitForPodReady(xfrmPod, xfrmPodNsn(c.Name), r.Get)
		if err != nil {
			logger.Error(err, "Error waiting for xfrm pod to be ready", "pod", xfrmPod.Name)
			return nil, err
		}
		
		resp, err := http.Get(fmt.Sprintf("http://%s:8080/pid", xfrmPod.Status.PodIP))
		if err != nil {
			logger.Error(err, "Couldn't request PID from xfrm pod", "xfrm-pod", xfrmPod.Name)
			return nil, err
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Error(err, "Couldn't read body of pid response", "xfrm-pod", xfrmPod.Name)
			return nil, err
		}

		prd := &comms.PidResponseData{}
		err = json.Unmarshal(out, prd)
		if err != nil {
			logger.Error(err, "Couldn't unmarshal body of pid response", "xfrm-pod", xfrmPod.Name)
			return nil, err
		}

		if prd.Error != "" {
			err = fmt.Errorf("%s", prd.Error)
			logger.Error(err, "Error in PID response from xfrm-pod")
			return nil, err
		}

		xfrmRequest := comms.XfrmRequestData{
			XfrmIfId: c.XfrmIfId,
			PID: prd.Pid,
		}

		charonUrl := fmt.Sprintf("http://%s:8080", charonPodIp)
		resp, err = comms.SendPost(charonUrl + "/xfrm", xfrmRequest)

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

		xfrmUrl := fmt.Sprintf("http://%s:8080", xfrmPod.Status.PodIP)
		resp, err = comms.SendPost(xfrmUrl + "/addRoutes", c)
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

	ripjson, _ := json.Marshal(c.RemoteIps)
	a := xfrmPod.Annotations
	stillValid := true
	stillValid = stillValid && a["ipman.dialo.ai/childName"] == c.Name
	stillValid = stillValid && a["ipman.dialo.ai/vxlanip"] == c.VxlanIP
	stillValid = stillValid && a["ipman.dialo.ai/xfrmip"] == c.XfrmIP
	stillValid = stillValid && a["ipman.dialo.ai/remoteips"] == string(ripjson)
	stillValid = stillValid && a["ipman.dialo.ai/xfrmid"] == strconv.FormatInt(int64(c.XfrmIfId), 10)

	return xfrmPod, nil

}

func (r *IpmanReconciler)createXfrmPod(c *ipmanv1.Child, nodeName string) (*corev1.Pod) {
	remoteIpsJSON, _ := json.Marshal(c.RemoteIps)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      XfrmPodName + "-" + c.Name,
			Namespace: "ims",
			Annotations: map[string]string{
				"ipman.dialo.ai/childName": c.Name,
				"ipman.dialo.ai/vxlanip": c.VxlanIP,
				"ipman.dialo.ai/xfrmip": c.XfrmIP,
				"ipman.dialo.ai/remoteips": string(remoteIpsJSON),
				"ipman.dialo.ai/xfrmid": strconv.FormatInt(int64(c.XfrmIfId), 10),
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			RestartPolicy: corev1.RestartPolicyNever,
			HostPID: true,
			SecurityContext: &corev1.PodSecurityContext{
				Sysctls: []corev1.Sysctl{
					{
						Name: "net.ipv4.ip_forward",
						Value: "1",
					},
					{
						Name: "net.ipv4.conf.all.rp_filter",
						Value: "0",
					},
					{
						Name: "net.ipv4.conf.default.rp_filter",
						Value: "0",
					},
					{
						Name: "net.ipv4.conf.all.arp_filter",
						Value: "1",
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name: "xfrm-container", 
					Image: "plan9better/xfrminion:latest",
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

