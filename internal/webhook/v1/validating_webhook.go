package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"

	ipmanv1 "dialo.ai/ipman/api/v1"
	u "dialo.ai/ipman/pkg/utils"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ValidatingWebhookHandler struct{
	Client client.Client
	Config rest.Config
}

// actionTypes
type creationAction struct{}
type deletionAction struct{}
type updateAction   struct{}
type unknownAction  struct{}

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

func validateIpmanDeletion(req *admissionv1.AdmissionRequest, workers []corev1.Pod, xfrm []corev1.Pod) (bool, error) {
	ipman := &ipmanv1.Ipman{}
	err := json.Unmarshal(req.OldObject.Raw, ipman)
	if err != nil {
		return false, fmt.Errorf("Couldn't unmarshal ipman: %w", err)
	}

	for _, p := range xfrm {
		if slices.ContainsFunc(ipman.Spec.Children, func (c ipmanv1.Child) bool {
			fmt.Println(c.Name,p.Annotations["ipman.dialo.ai/childName"], c.Name == p.Annotations["ipman.dialo.ai/childName"])
			return c.Name == p.Annotations["ipman.dialo.ai/childName"]
		}){
			if !canDeleteXfrm(&p, workers){
				podNames := []string{}
				for _, pod := range workers {
					podNames = append(podNames, pod.Name)
				}
				return false, fmt.Errorf("Worker pods use xfrm that belongs to this ipman: %v", podNames)
			}
		}
	}

	return true, nil
}

func validateIpmanCreation(ipman ipmanv1.Ipman, other []ipmanv1.Ipman, pods []corev1.Pod)(bool, error){
   	for _, obj := range other {
   		if obj.Spec.Name == ipman.Spec.Name{
   			return false, fmt.Errorf("Ipman with that name already exists %s == %s", obj.Spec.Name, ipman.Spec.Name)
   		}
   	}
   	for _, p := range pods {
   		if p.Annotations["ipman.dialo.ai/ipmanName"] == ipman.Spec.Name {
   			return false, fmt.Errorf("There are existing pods with this ipman annotation: %s@%s", p.ObjectMeta.Name, p.ObjectMeta.Namespace)
   		}
   	}
   	// no conflicts: allow creation
   	return true, nil
}
func validateIpmanUpdate(new ipmanv1.Ipman, old ipmanv1.Ipman, pods []corev1.Pod)(bool, error){
		return false, fmt.Errorf("UNIMPLEMENTED")
}

func getAction(req *admissionv1.AdmissionRequest) any {
	if len(req.OldObject.Raw) == 0  && len(req.Object.Raw) != 0 {
		return creationAction{}
	}

	if len(req.OldObject.Raw) != 0 && len(req.Object.Raw) == 0 {
		return deletionAction{}
	} 

	if len(req.OldObject.Raw) != 0 && len(req.Object.Raw) != 0 {
		return updateAction{}
	}

	return unknownAction{}
}

func extractXfrmPods(pods []corev1.Pod) []corev1.Pod {
	annotatedPods := []corev1.Pod{}
	for _, p := range pods {
		if _, ok := p.Annotations["ipman.dialo.ai/childName"]; ok && p.Namespace == "ims"{
			annotatedPods = append(annotatedPods, p)
		}
	}
	return annotatedPods
}

func(wh *ValidatingWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request){
	ctx := context.Background()
	logger := log.FromContext(ctx,"webhook", true)

	in, err := u.ParseRequest(*r)
	if err != nil {
		err = fmt.Errorf("Couldn't parse request for validatoin: %w", err)
		writeResponseDenied(w, in, err.Error())
	}

	action := getAction(in.Request)
	logger.Info("validating", "action", reflect.TypeOf(action))

	allPods := &corev1.PodList{}
	err = wh.Client.List(ctx, allPods)
	if err != nil {
		err = fmt.Errorf("Error listing pods for creation validation: %w", err)
		writeResponseDenied(w, in, err.Error())
		return
	}

	verdict := false
	switch action.(type) {
		case creationAction:
			logger.Info("Create action")
			ipmen := &ipmanv1.IpmanList{}
			err = wh.Client.List(ctx, ipmen)
			if err != nil {
				err = fmt.Errorf("Couldn't list ipmen: %w", err)
				writeResponseDenied(w, in, err.Error())
				return
			}
			// unmarshal new Ipman object from request
			newIpman := &ipmanv1.Ipman{}
			if err = json.Unmarshal(in.Request.Object.Raw, newIpman); err != nil {
				err = fmt.Errorf("Couldn't unmarshal ipman for creation: %w", err)
				writeResponseDenied(w, in, err.Error())
				return
			}
			verdict, err = validateIpmanCreation(*newIpman, ipmen.Items, allPods.Items)

		case deletionAction:

			xfrmPods := extractXfrmPods(allPods.Items)
			verdict, err = validateIpmanDeletion(in.Request, allPods.Items, xfrmPods)
			logger.Info("Verdict", "Verdict", verdict, "error", err)

		case updateAction:
			old := ipmanv1.Ipman{}
			err = json.Unmarshal(in.Request.OldObject.Raw, &old)
			if err != nil {
				err = fmt.Errorf("Couldn't unmarshal old object: %w", err)
				writeResponseDenied(w, in, err.Error())
				return
			}
			new := ipmanv1.Ipman{}
			err = json.Unmarshal(in.Request.OldObject.Raw, &new)
			if err != nil {
				err = fmt.Errorf("Couldn't unmarshal new object: %w", err)
				writeResponseDenied(w, in, err.Error())
				return
			}
			verdict, err = validateIpmanUpdate(new, old, allPods.Items)

		default:
			logger.Info("unknown action")
			verdict = false
			err = fmt.Errorf("Couldn't determine action being taken")
	}

	if verdict {
		writeResponseNoPatch(w, in)
		return
	}

	writeResponseDenied(w, in, err.Error())
}

func canDeleteXfrm(xfrm *corev1.Pod, workers []corev1.Pod) bool {
	fmt.Println("Can delete? ", workers)
	dependentPods := slices.DeleteFunc(workers, func (p corev1.Pod)bool {
		cn, okChild := p.Annotations["ipman.dialo.ai/childName"]
		_, okImn := p.Annotations["ipman.dialo.ai/ipmanName"]
		_, okPn := p.Annotations["ipman.dialo.ai/poolName"]
		return !(okChild && okImn && okPn && cn == xfrm.Annotations["ipman.dialo.ai/childName"])
	})
	fmt.Println("Can delete end? ", dependentPods)
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

