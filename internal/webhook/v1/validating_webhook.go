package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	u "dialo.ai/ipman/pkg/utils"
	"github.com/jrhouston/k8slock"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ValidatingWebhookHandler struct{
	Client client.Client
	Config rest.Config
}

func writeResponseNoPatch(w http.ResponseWriter, in *admissionv1.AdmissionReview){
		w.Header().Add("Content-Type", "application/json")
		r := noPatchResponse(in)
		rjson, err := json.Marshal(r)
		if err != nil {
			fmt.Println("Error marshalling response for no patch: ", err)
			return
		}
		w.Write(rjson)
}

func writeResponseDenied(w http.ResponseWriter, in *admissionv1.AdmissionReview){
		w.Header().Add("Content-Type", "application/json")
		r := deniedResponse(in)
		rjson, err := json.Marshal(r)
		if err != nil {
			fmt.Println("Error marshalling response for denied: ", err)
			return
		}
		w.Write(rjson)
}

func(wh *ValidatingWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request){
	in, err := u.ParseRequest(*r)
	if err != nil {
		println("Error parsing request: ", err)
	}
	ctx := context.Background()
	logger := log.FromContext(ctx,"webhook", true, "PodName", in.Request.Name)

	if(in.Request.Kind.Kind != "Pod") {
		writeResponseNoPatch(w, in)
		return 
	}
	var pod corev1.Pod
	json.Unmarshal(in.Request.Object.Raw, &pod)
	childName, childOk := pod.Annotations["ipman.dialo.ai/childName"]
	connName, connOk := pod.Annotations["ipman.dialo.ai/ipmanName"]
	poolName, poolOk := pod.Annotations["ipman.dialo.ai/poolName"]
	if !(childOk && connOk && poolOk) {
		writeResponseNoPatch(w, in)
		return 
	} 

	clientSet, err := kubernetes.NewForConfig(&wh.Config)
	if err != nil {
		logger.Error(err, "Couldn't create clientset for managing leases")
		writeResponseDenied(w, in)
	}
	var locker *k8slock.Locker 
	for {
		locker, err = k8slock.NewLocker("ipman-" + connName + "-lease-lock",
			k8slock.TTL(5 * time.Second),
			k8slock.Namespace("ims"),
			k8slock.Clientset(clientSet),
		)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				n := ((rand.Int() % 10) + 1) * 100
				logger.Info("Lease already exists, sleeping randomly", "time", n)
				time.Sleep(time.Duration(n) * time.Millisecond )
			} else {
				logger.Error(err, "Couldn't create a lease locker instance")
				writeResponseDenied(w, in)
				return
			}
		} else {
			break
		}
		
	}

	// prevent race conditions for IP address
	// from ipman status with leases
	locker.Lock()
	ipman := &ipmanv1.Ipman{}
	nsn := types.NamespacedName{
		Namespace: "",
		Name: connName,
	}
	err = wh.Client.Get(ctx, nsn, ipman)
	if err != nil {
		fmt.Println("Couldn't find a matching connection, aborting", err)
		writeResponseDenied(w, in)
		return
	}

	var ipmanChild *ipmanv1.Child
	for _, c := range ipman.Spec.Children {
		if c.Name == childName {
			ipmanChild = &c
			break
		}
	}

	if ipmanChild == nil {
		fmt.Println("Couldn't find a matching child, aborting\n", "children:", ipman.Spec.Children)
		writeResponseDenied(w, in)
		return
		
	}

	if len(ipman.Status.FreeIPs[ipmanChild.Name][poolName]) == 0 {
		logger.Info("There are no free IP addresses for requested child. Denying request for pod.\n","child", ipmanChild.Name, "status", ipman.Status)
		writeResponseDenied(w, in)
		return
	}

	pool, ok := ipman.Status.FreeIPs[ipmanChild.Name][poolName]
	if !ok {
		logger.Error(fmt.Errorf("Error, couldn't find pool"),"Pool not found in ipman" ,"annotations", pod.Annotations["ipman.dialo.ai/poolName"], "child", ipmanChild.Name, "ipman", ipman.Name)
		writeResponseDenied(w, in)
		return
	}

	// TODO: change type of spec.children to a map instead of list
	out, _ := json.Marshal(ipmanChild.RemoteIps)
	annotations := map[string]string {
		"ipman.dialo.ai/vxlanIp": pool[0],
		"ipman.dialo.ai/xfrmIp": ipmanChild.XfrmIP,
		"ipman.dialo.ai/interfaceId": strconv.FormatInt(int64(ipmanChild.XfrmIfId), 10),
		// TODO: find out if the remote ip being added or removed
		// affects this pod and restart it then??
		// maybe annotation on creation if not present
		// always restarted
		"ipman.dialo.ai/remoteIps": string(out),
		"ipman.dialo.ai/xfrmUnderlyingIp": ipman.Status.XfrmGatewayIPs[ipmanChild.Name],
	}
	patch := patch(&pod, pool[0], ipman.Status.XfrmGatewayIPs[ipmanChild.Name], annotations, *ipmanChild)
	ipman.Status.FreeIPs[ipmanChild.Name][poolName] = slices.Delete(ipman.Status.FreeIPs[ipmanChild.Name][poolName], 0, 1)
	if ipman.Status.PendingIPs == nil {
		ipman.Status.PendingIPs = map[string]string{}
	}
	ipman.Status.PendingIPs[pool[0]] = pod.Name + "-" + pod.Namespace
	err = wh.Client.Update(ctx, ipman)
	if err != nil {
		logger.Error(err, "Couldn't update status of ipman in webhook")
		locker.Unlock()
		writeResponseDenied(w, in)
		return
	}

	resp := response(patch, in)
	respJson, err := json.Marshal(resp)
	if err != nil {
		println("Error marshalling response: ", err)
	}
	logger.Info("Applying patch", "pod", pod.Name)
	
	w.Header().Add("Content-Type", "application/json")
	w.Write(respJson)
}

func initContainer(child ipmanv1.Child, gateway string, ip string) *corev1.Container{
	// out, _ := json.Marshal(child.RemoteIps)
	remoteIps := strings.Join(child.RemoteIps, ",")
	return &corev1.Container{
		Name: "iface-request",
		Image: "plan9better/vxlandlord:latest",
		ImagePullPolicy: corev1.PullAlways,
		SecurityContext: createNetAdminSecurityContext(),
		// TODO: as a config map
		Env: []corev1.EnvVar{
			{
				Name: "VXLAN_IP",
				Value: ip,
			},
			{
				Name: "XFRM_GATEWAY_IP",
				Value: gateway,
			},
			{
				Name: "INTERFACE_ID",
				Value: strconv.FormatInt(int64(child.XfrmIfId), 10),
			},
			{
				Name: "XFRM_IP",
				Value: child.XfrmIP,
			},
			{
				Name: "REMOTE_IPS",
				Value: remoteIps,
			},
		},
	}
}

type jsonPatch struct {
	Op string `json:"op"`
	Path string `json:"path"`
	Value any `json:"value"`
}

func createEnvPatch(p *corev1.Pod, ip string) []jsonPatch {
	patch := []jsonPatch{}
	for i := range p.Spec.Containers {
		env := []corev1.EnvVar{
			{
				Name:  "VXLAN_IP",
				Value: ip,
			},
		}
		patch = append(patch, jsonPatch{
			Op:    "add",
			Path:  fmt.Sprintf("/spec/containers/%d/env", i),
			Value: env,
		})
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

func createAnnotationPatch(annotations map[string]string)[]jsonPatch{
	patches := []jsonPatch{}
	for key, value := range annotations {
		// replace '/' with '~1' which jsonPatch replaces back to '/'
		processedKey := strings.Replace(key, "/", "~1", 1)
		patches = append(patches, jsonPatch{
			Op: "add",
			Path: "/metadata/annotations/" + processedKey,
			Value: value,
		})
	}
	return patches
}

func createInitContainerPatch(p *corev1.Pod,child ipmanv1.Child, gateway string, ip string) *jsonPatch {
	if len(p.Spec.InitContainers) == 0 {
		return &jsonPatch{
			Op: "add",
			Path: "/spec/initContainers",
			Value: []corev1.Container{*initContainer(child, gateway, ip)},
		}
	}
	return &jsonPatch{
		Op: "add",
		Path: "/spec/initContainers/-",
		Value: *initContainer(child, gateway, ip),
	}
}

// func createNodeSelectorPatch(nodeName string) *jsonPatch {
// 	return &jsonPatch{
// 		Op: "add",
// 		Path: "/spec/nodeSelector",
// 		Value: map[string]string{
// 			"kubernetes.io/hostname": nodeName,
// 		},
// 	}
// }

func patch(p *corev1.Pod, ip string, gateway string, annotations map[string]string, child ipmanv1.Child) []byte {
	patch := []jsonPatch{}
	
	ap := createAnnotationPatch(annotations)
	if ap == nil {
		fmt.Println("Error creating annotation patch. Annotations:", p.ObjectMeta.Annotations)
		return []byte{}
	}
	patch = append(patch, ap...)

	icp := createInitContainerPatch(p, child, gateway, ip)
	patch = append(patch, *icp)

	ep := createEnvPatch(p, ip)
	if ep == nil {
		fmt.Println("Error creating env patch. Containers:", p.Spec.Containers)
		return []byte{}
	}
	patch = append(patch, ep...)

	out, err := json.Marshal(patch)
	if err != nil {
		println("Error marshalling json patch: ", err)
	}

	return out
}

func noPatchResponse(in *admissionv1.AdmissionReview) *admissionv1.AdmissionReview{
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID: in.Request.UID,
			Allowed: true,
		},
	}

}
func deniedResponse(in *admissionv1.AdmissionReview) *admissionv1.AdmissionReview{
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID: in.Request.UID,
			Allowed: false,
		},
	}

}

func response(patch []byte,in *admissionv1.AdmissionReview) *admissionv1.AdmissionReview{
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID: in.Request.UID,
			Allowed: true,
			Patch: patch,
			PatchType: &pt,
		},
	}
}

// TODO: this is more or less duplicated in ipman_controller.go
func createNetAdminSecurityContext() *corev1.SecurityContext {
	privEsc := false
	return &corev1.SecurityContext{
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},

		AllowPrivilegeEscalation: &privEsc,
		Capabilities: &corev1.Capabilities{
			Add: []corev1.Capability{"NET_ADMIN"},
		},
	}
}
