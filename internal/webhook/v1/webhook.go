package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
)

type WebhookHandler struct{
}

func(wh *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request){
	in, err := parseRequest(*r)
	if err != nil {
		println("Error parsing request: ", err)
	}

	var pod corev1.Pod
	err = json.Unmarshal(in.Request.Object.Raw, &pod)
	if err != nil {
		println("Error unmarshaling ")
	}
	patch := patch()
	resp := response(patch, in)

	respJson, err := json.Marshal(resp)
	if err != nil {
		println("Error marshalling response: ", err)
	}
	
	w.Header().Add("Content-Type", "application/json")
	w.Write(respJson)
}

func initContainer() *corev1.Container{
	return &corev1.Container{
		Name: "some-init-container",
		Image: "ubuntu:latest",
		Command: []string{"bash", "-c", "while true; do sleep 10000; done"},
	}
}

func patch() []byte {
	cont, err := json.Marshal(initContainer())
	patch := (fmt.Appendf([]byte{}, `
[
	{
		"op": "add",
		"path": "/spec/initContainers",
		"value": [
			%s
		]
	}
]`,cont))

	if err != nil {
		println("Error marshalling json patch: ", err)
	}

	return patch
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
