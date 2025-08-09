package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/internal/controller"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// createAdmissionRequest creates a new admission request for testing
func createAdmissionRequest(obj runtime.Object, operation admissionv1.Operation) *admissionv1.AdmissionRequest {
	objRaw, _ := json.Marshal(obj)
	return &admissionv1.AdmissionRequest{
		UID:       "test-uid",
		Kind:      metav1.GroupVersionKind{Kind: "IPSecConnection"},
		Operation: operation,
		Object:    runtime.RawExtension{Raw: objRaw},
	}
}

// createAdmissionRequestWithOldObject creates a new admission request with both old and new objects
func createAdmissionRequestWithOldObject(oldObj, newObj runtime.Object, operation admissionv1.Operation) *admissionv1.AdmissionRequest {
	oldObjRaw, _ := json.Marshal(oldObj)
	newObjRaw, _ := json.Marshal(newObj)
	return &admissionv1.AdmissionRequest{
		UID:       "test-uid",
		Kind:      metav1.GroupVersionKind{Kind: "IPSecConnection"},
		Operation: operation,
		OldObject: runtime.RawExtension{Raw: oldObjRaw},
		Object:    runtime.RawExtension{Raw: newObjRaw},
	}
}

// createAdmissionReview wraps an admission request in an admission review
func createAdmissionReview(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionReview {
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Request: req,
	}
}

// createTestPod creates a pod for testing
func createTestPod(name, namespace string, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test-image",
				},
			},
		},
	}
}

// createTestIPSecConnection creates an IPSecConnection for testing
func createTestIPSecConnection(name, namespace string) *ipmanv1.IPSecConnection {
	return &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       name,
			RemoteAddr: "10.0.0.1",
			LocalAddr:  "10.0.0.2",
			LocalId:    "10.0.0.2",
			RemoteId:   "10.0.0.1",
			SecretRef: ipmanv1.SecretRef{
				Name:      "secret-1",
				Namespace: "default",
				Key:       "psk",
			},
			Children: map[string]ipmanv1.Child{
				"child1": {
					Name: "child1",
					Extra: map[string]string{
						"esp_proposals": "aes256-sha256-ecp256",
					},
					LocalIPs:  []string{"192.168.1.0/24"},
					RemoteIPs: []string{"192.168.2.0/24"},
					XfrmIP:    "192.168.1.1/24",
					VxlanIP:   "192.168.1.2/24",
					XfrmIfId:  101,
					IpPools: map[string][]string{
						"pool1": {"192.168.1.3/24", "192.168.1.4/24"},
					},
				},
			},
			NodeName: "test-node",
		},
		Status: ipmanv1.IPSecConnectionStatus{
			FreeIPs: map[string]map[string][]string{
				"child1": {
					"pool1": {"192.168.1.3/24", "192.168.1.4/24"},
				},
			},
			CharonProxyIP: "10.0.0.10",
			XfrmGatewayIPs: map[string]string{
				"child1": "10.0.0.11",
			},
			PendingIPs: map[string]string{},
		},
	}
}

// createMockMutatingWebhookHandler creates a mock mutating webhook handler
func createMockMutatingWebhookHandler(objects ...client.Object) *MutatingWebhookHandler {
	scheme := runtime.NewScheme()
	ipmanv1.AddToScheme(scheme)
	var client client.Client
	if len(objects) != 0 {
		client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	} else {
		fake.NewClientBuilder().WithScheme(scheme).Build()
	}

	return &MutatingWebhookHandler{
		Client: client,
		Config: rest.Config{},
		Env: controller.Envs{
			NamespaceName:            "ipman-system",
			VxlandlordImage:          "test-vxlandlord-image",
			IsTest:                   true,
			WaitForPodTimeoutSeconds: 1,
		},
	}
}

// createMockValidatingWebhookHandler creates a mock validating webhook handler
func createMockValidatingWebhookHandler() *ValidatingWebhookHandler {
	scheme := runtime.NewScheme()
	ipmanv1.AddToScheme(scheme)

	return &ValidatingWebhookHandler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Config: rest.Config{},
		Env: controller.Envs{
			NamespaceName:            "ipman-system",
			IsTest:                   true,
			WaitForPodTimeoutSeconds: 1,
		},
	}
}

// testWebhookRequest sends a request to a webhook handler and returns the response
func testWebhookRequest(t *testing.T, handler http.Handler, review *admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	// Serialize the admission review
	reviewBytes, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("Failed to marshal admission review: %v", err)
	}

	// Create a request
	req := httptest.NewRequest("POST", "/", bytes.NewBuffer(reviewBytes))
	req.Header.Set("Content-Type", "application/json")

	// Create a recorder to capture the response
	recorder := httptest.NewRecorder()

	// Send the request to the handler
	handler.ServeHTTP(recorder, req)

	// Check response code
	if recorder.Code != http.StatusOK {
		t.Fatalf("Handler returned wrong status code: got %v want %v",
			recorder.Code, http.StatusOK)
	}

	// Parse the response
	var responseReview admissionv1.AdmissionReview
	respBytes, _ := io.ReadAll(recorder.Body)
	if err := json.Unmarshal(respBytes, &responseReview); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, string(respBytes))
	}

	return &responseReview
}

// extractPatchOperations extracts patch operations from an admission response
func extractPatchOperations(t *testing.T, response *admissionv1.AdmissionReview) []jsonPatch {
	if response.Response == nil || response.Response.Patch == nil {
		return nil
	}

	var patches []jsonPatch
	if err := json.Unmarshal(response.Response.Patch, &patches); err != nil {
		t.Fatalf("Failed to unmarshal patches: %v", err)
	}

	return patches
}

// assertPatchContains checks if a patch contains a specific operation on a path
func assertPatchContains(t *testing.T, patches []jsonPatch, operation, path string) {
	for _, patch := range patches {
		if patch.Op == operation && patch.Path == path {
			return
		}
	}
	t.Errorf("Patch does not contain %s operation on path %s", operation, path)
}
