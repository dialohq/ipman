package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	ipmanv1 "dialo.ai/ipman/api/v1"
	u "dialo.ai/ipman/pkg/utils"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
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

func writeResponseDenied(w http.ResponseWriter, in *admissionv1.AdmissionReview, reason ...string){
		w.Header().Add("Content-Type", "application/json")
		r := deniedResponse(in, reason...)
		rjson, err := json.Marshal(r)
		if err != nil {
			fmt.Println("Error marshalling response for denied: ", err)
			return
		}
		w.Write(rjson)
}

func(wh *ValidatingWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request){
	ctx := context.Background()
	logger := log.FromContext(ctx,"webhook", true)

	in, err := u.ParseRequest(*r)
	if err != nil {
		logger.Error(err, "Error parsing request")
	}

	if in.Request.Kind.Kind != "Pod" {
		ipman := &ipmanv1.Ipman{}
		err = json.Unmarshal(in.Request.Object.Raw, ipman)
		if err != nil {
			logger.Error(err, "Couldn't unmarshal ipman", "data", string(in.Request.OldObject.Raw))
			writeResponseNoPatch(w, in)
			return
		}

		s := labels.SelectorFromSet(map[string]string{
			"ipman.dialo.ai/xfrm": in.Request.Name,
		})
		xfrmPods := &corev1.PodList{}
		err = wh.Client.List(ctx, xfrmPods, &client.ListOptions{
			LabelSelector: s,
		})
		if err != nil {
			logger.Error(err, "Error fetching list of xfrm pods to validate ipman")
			writeResponseDenied(w, in)
			return
		}
		workers := &corev1.PodList{}
		err = wh.Client.List(ctx, workers)
		if err != nil {
			logger.Error(err, "Error listing pods to check if xfrm pod can be deleted in ipman")
			writeResponseDenied(w, in)
			return
		}

		for _, p := range xfrmPods.Items {
			if !slices.ContainsFunc(ipman.Spec.Children, func (c ipmanv1.Child) bool {
				return c.Name == p.Annotations["ipman.dialo.ai/childName"]
			}){
				if !canDeleteXfrm(&p, workers.Items){
					podNames := []string{}
					for _, pod := range workers.Items {
						podNames = append(podNames, pod.Name)
					}
					writeResponseDenied(w, in, fmt.Sprintf("Workers use child that's to be deleted: %v", podNames))
					return
				}
			}
		}
		writeResponseNoPatch(w, in)
		return
	}

	pod := &corev1.Pod{}
	deletion := false
	if len(in.Request.OldObject.Raw) != 0 {
		err = json.Unmarshal(in.Request.OldObject.Raw, pod)
		if err != nil {
			logger.Error(err, "Couldn't unmarshal old pod", "data", string(in.Request.OldObject.Raw))
			writeResponseNoPatch(w, in)
			return
		}
		deletion = true
	} else {
		err = json.Unmarshal(in.Request.Object.Raw, pod)
		if err != nil {
			logger.Error(err, "Couldn't unmarshal new pod", "data", string(in.Request.Object.Raw))
			writeResponseNoPatch(w, in)
			return
		}
	}


	childName, childOk := pod.Annotations["ipman.dialo.ai/childName"]
	if !childOk {
		writeResponseNoPatch(w, in)
		return 
	} 

	isXfrm := false
	sn := strings.Split(pod.Name, "-")
	if len(sn) > 3  && sn[0] == "xfrm" && sn[1] == "pod" && sn[2] == childName{
		isXfrm = true
	}

	if isXfrm && !deletion {
		writeResponseNoPatch(w, in)
		return
	}

	if isXfrm && deletion {
		pods := &corev1.PodList{}
		err = wh.Client.List(ctx, pods)
		if err != nil {
			logger.Error(err, "Error listing pods to check if xfrm pod can be deleted")
			writeResponseDenied(w, in)
			return
		}
		if canDeleteXfrm(pod, pods.Items) {
			writeResponseDenied(w, in)
			return
		}

		writeResponseNoPatch(w, in)
		return
	}


	if !isXfrm && !deletion{
		imn, ok := pod.Annotations["ipman.dialo.ai/ipmanName"]
		if !ok {
			logger.Error(fmt.Errorf("not found"), "Pod to be created doesn't have ipman name annotation")
			writeResponseDenied(w, in, "No annotation")
			return
		}
		ipman := &ipmanv1.Ipman{}
		nsn := types.NamespacedName{
			Namespace: "",
			Name: imn,
		}
		err = wh.Client.Get(ctx, nsn , ipman)
		if err != nil {
			logger.Error(err, "Couldn't fetch ipman", "nsn", nsn)
			writeResponseDenied(w, in, "Error fetching ipman")
			return
		}

		if ipman.Status.CharonProxyIP == "" {
			writeResponseDenied(w, in, "No charon proxy ip")
			return
		}
		if ip, ok := ipman.Status.XfrmGatewayIPs[childName]; !ok || ip == "" {
			writeResponseDenied(w, in, "No xfrm gateway ip")
			return
		}
	}

	writeResponseNoPatch(w, in)
}

func canDeleteXfrm(xfrm *corev1.Pod, workers []corev1.Pod) bool {
	dependentPods := slices.DeleteFunc(workers, func (p corev1.Pod)bool {
		cn, okChild := p.Annotations["ipman.dialo.ai/childName"]
		_, okImn := p.Annotations["ipman.dialo.ai/ipmanName"]
		_, okPn := p.Annotations["ipman.dialo.ai/poolName"]
		return !(okChild && okImn && okPn && cn == xfrm.Annotations["ipman.dialo.ai/childName"])
	})
	return len(dependentPods) == 0
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
func deniedResponse(in *admissionv1.AdmissionReview, reasonList ...string) *admissionv1.AdmissionReview{
	reason := "No reason provided."
	if len(reasonList) != 0 {
		reason = reasonList[0]
	}
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID: in.Request.UID,
			Allowed: false,
			Result: &metav1.Status{
				TypeMeta: metav1.TypeMeta{},
				Message: reason,
			},
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
