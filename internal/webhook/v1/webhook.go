package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"github.com/jrhouston/k8slock"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type WebhookHandler struct{
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

func(wh *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request){
	
	in, err := parseRequest(*r)
	if err != nil {
		println("Error parsing request: ", err)
	}
	ctx := context.Background()
	logger := log.FromContext(ctx,"webhook", true, "PodName", in.Request.Name)

	if(in.Request.Kind.Kind != "Pod"){
		writeResponseNoPatch(w, in)
		return 
	}
	var pod corev1.Pod
	json.Unmarshal(in.Request.Object.Raw, &pod)
	cn, ok := pod.Annotations["ipman.dialo.ai/childName"]
	if !ok {
		writeResponseNoPatch(w, in)
		return 
	} 

	// TODO: if not specified probably only 1
	pn, ok := pod.Annotations["ipman.dialo.ai/poolName"]
	if !ok {
		writeResponseNoPatch(w, in)
		return 
	} 

	ipmans := &ipmanv1.IpmanList{}
	err = wh.Client.List(ctx, ipmans)
	if err != nil {
		fmt.Println("Error reading list of ipmans: ", err)
		return
	}

	var ipmanChild *ipmanv1.Child
	var ipman *ipmanv1.Ipman
	for _, it := range ipmans.Items {
		for _, c := range it.Spec.Children {
			if c.Name == cn {
				fmt.Println("Found matching child")
				ipmanChild = &c
				ipman = &it
				break
			}
			
		}
	}

	if ipman == nil {
		fmt.Println("Couldn't find a matching connection, aborting")
		writeResponseDenied(w, in)
		return
	}
	fmt.Println("Ipman is not nil")

	clientSet, err := kubernetes.NewForConfig(&wh.Config)
	if err != nil {
		logger.Error(err, "Couldn't create clientset for managing leases")
		writeResponseDenied(w, in)
	}
	locker, err := k8slock.NewLocker("ipman-" + ipman.Name + "-lease-lock",
		k8slock.TTL(5 * time.Second),
		k8slock.Namespace("ims"),
		k8slock.Clientset(clientSet),
	)
	if err != nil {
		logger.Error(err, "Couldn't create a lease locker instance")
	}

	// prevent race conditions for IP address
	// from ipman status with leases
	locker.Lock()
	if len(ipman.Status.FreeIPs[ipmanChild.Name][pn]) == 0 {
		fmt.Printf("There are no free IP addresses for child %s, denying request for pod\n", ipmanChild.Name)
		writeResponseDenied(w, in)
		return
	}
	fmt.Println("there is an ip address available")

	pool, ok := ipman.Status.FreeIPs[ipmanChild.Name][pn]
	if !ok {
		fmt.Println("Error, couldn't find pool", pod.Annotations["ipman.dialo.ai/poolName"], "in child ", ipmanChild.Name)
	}
	patch := patch(&pod, pool[0], ipman.Status.XfrmGatewayIPs[ipmanChild.Name], ipman.Spec.NodeName)
	ipman.Status.FreeIPs[ipmanChild.Name][pn] = slices.Delete(ipman.Status.FreeIPs[ipmanChild.Name][pn], 0, 1)
	if ipman.Status.PendingIPs == nil {
		ipman.Status.PendingIPs = map[string]string{}
	}
	ipman.Status.PendingIPs[pool[0]] = pod.Name + "-" + pod.Namespace
	err = wh.Client.Update(ctx, ipman)
	if err != nil {
		logger.Error(err, "Couldn't update status of ipman")
		locker.Unlock()
		writeResponseDenied(w, in)
		return
	}

	
	fmt.Println("created patch")
	resp := response(patch, in)
	fmt.Println("created response")

	respJson, err := json.Marshal(resp)
	if err != nil {
		println("Error marshalling response: ", err)
	}
	fmt.Println("marshaled response", string(respJson))

	
	w.Header().Add("Content-Type", "application/json")
	w.Write(respJson)
}

// func encodeSecret(im *ipmanv1.Child, gwIP ,vxIP string) *corev1.Secret{
// 	d := map[string][]byte{
// 		"if_id": []byte(fmt.Sprint(im.XfrmIfId)),
// 		"vxlan_ip": []byte(vxIP),
// 		"gateway_ip": []byte(gwIP),
// 		"remote_ts": []byte(im.RemoteTs),
// 	}
// 	return &corev1.Secret{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: "ipman-secret-" + im.Name,
// 			Namespace: "ims",
// 		},
// 		Data: d,
// 	}
// }

func initContainer(ip string, cn string, gateway string) *corev1.Container{
	return &corev1.Container{
		Name: "iface-request",
		Image: "plan9better/vxlandlord:latest",
		ImagePullPolicy: corev1.PullAlways,
		SecurityContext: createNetAdminSecurityContext(),
		Env: []corev1.EnvVar{
			{
				Name: "VXLAN_IP",
				Value: ip,
			},
			{
				Name: "CHILD_NAME",
				Value: cn,
			},
			{
				Name: "XFRM_GATEWAY",
				Value: gateway,
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

func createAnnotationPatch(p *corev1.Pod, ip string)*jsonPatch{
	if _, ok := p.ObjectMeta.Annotations["ipman.dialo.ai/vxlan"]; !ok {
		return &jsonPatch{
			Op: "add",
			Path: "/metadata/annotations/ipman.dialo.ai~1vxlan",
			Value: ip,
		}
	}
	return nil
}

func createInitContainerPatch(p *corev1.Pod, ip string, childname string, gateway string) *jsonPatch {
	if len(p.Spec.InitContainers) == 0 {
		return &jsonPatch{
			Op: "add",
			Path: "/spec/initContainers",
			Value: []corev1.Container{*initContainer(ip, childname, gateway)},
		}
	}
	return &jsonPatch{
		Op: "add",
		Path: "/spec/initContainers/-",
		Value: *initContainer(ip, childname, gateway),
	}
}

// TODO: this can't be here,
// we can't override restart policy
// remove before release
func createRestartPolicyPatch() *jsonPatch {
	return &jsonPatch{
		Op: "add",
		Path: "/spec/restartPolicy",
		Value: "Never",
	}
}

func createNodeSelectorPatch(nodeName string) *jsonPatch {
	return &jsonPatch{
		Op: "add",
		Path: "/spec/nodeSelector",
		Value: map[string]string{
			"kubernetes.io/hostname": nodeName,
		},
	}
}

func patch(p *corev1.Pod, ip string, gateway string, nodeName string) []byte {
	patch := []jsonPatch{}
	nsp := createNodeSelectorPatch(nodeName)
	patch = append(patch, *nsp)
	
	ap := createAnnotationPatch(p, ip)
	if ap == nil {
		fmt.Println("Error creating annotation patch. Annotations:", p.ObjectMeta.Annotations)
		return []byte{}
	}
	patch = append(patch, *ap)

	icp := createInitContainerPatch(p, ip, p.Annotations["ipman.dialo.ai/childName"], gateway)
	patch = append(patch, *icp)

	ep := createEnvPatch(p, ip)
	if ep == nil {
		fmt.Println("Error creating env patch. Containers:", p.Spec.Containers)
		return []byte{}
	}
	patch = append(patch, ep...)

	rp := createRestartPolicyPatch()
	patch = append(patch, *rp)

	// conts := ""
	// TODO: check if exists and don't replace
	// for i := range p.Spec.Containers {
	// 	conts += fmt.Sprintf(`{
	// 	"op": "add",
	// 	"path": "/spec/containers/%d/env",
	// 	"value": [
	// 		{
	// 			"name": "%s",
	// 			"value": "%s"
	// 		}
	// 	]
	// }`, i, "VXLAN_IP", ip)
	// }

	// contJson, err := json.Marshal(initContainer())
	// if err != nil {
	// 	fmt.Println("Error marshalling init container for patch:", err)
	// }
	// TODO: node selector
	// annotation := fmt.Sprintf(`{
	// 	"op": "add",
	// 	"path": "/metadata/annotations/ipman.dialo.ai~1vxlan",
	// 	"value": "%s"
	// }`, ip)
// 	patch := (fmt.Appendf([]byte{}, `
// [
// 	%s,
// 	%s,
// 	{
// 		"op": "add",
// 		"path": "/spec/initContainers",
// 		"value": [
// 			%s
// 		]
// 	}
// ]`,annotation, conts, contJson))

	out, err := json.Marshal(patch)
	if err != nil {
		println("Error marshalling json patch: ", err)
	}
	fmt.Println("Applying patch: ", string(out))

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


// https://github.com/slackhq/simple-kubernetes-webhook/blob/main/main.go
func parseRequest(r http.Request) (*admissionv1.AdmissionReview, error) {
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("Content-Type: %q should be %q",
			r.Header.Get("Content-Type"), "application/json")
	}

	bodybuf := new(bytes.Buffer)
	bodybuf.ReadFrom(r.Body)
	body := bodybuf.Bytes()

	if len(body) == 0 {
		return nil, fmt.Errorf("admission request body is empty")
	}

	var a admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("could not parse admission review request: %v", err)
	}

	if a.Request == nil {
		return nil, fmt.Errorf("admission review can't be used: Request field is nil")
	}

	return &a, nil
}
