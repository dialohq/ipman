package v1

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	ipmanv1 "dialo.ai/ipman/api/v1"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMutatingWebhookHandler_ServeHTTP(t *testing.T) {
	tests := []struct {
		name                string
		requestPod          *corev1.Pod
		existingIPSecConn   *ipmanv1.IPSecConnection
		expectedAllowed     bool
		expectedPatchFields []string
	}{
		{
			name: "Non-IPMan pod should pass through without patches",
			requestPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular-pod",
					Namespace: "default",
				},
			},
			existingIPSecConn:   nil,
			expectedAllowed:     true,
			expectedPatchFields: nil,
		},
		{
			name: "Pod with IPMan annotations but missing fields should be denied",
			requestPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "partial-ipman-pod",
					Namespace: "default",
					Annotations: map[string]string{
						ipmanv1.AnnotationIpmanName: "test-ipman",
						// Missing childName and poolName
					},
				},
			},
			existingIPSecConn:   nil,
			expectedAllowed:     false,
			expectedPatchFields: nil,
		},
		{
			name: "Valid IPMan pod should be patched with required fields",
			requestPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-ipman-pod",
					Namespace: "default",
					Annotations: map[string]string{
						ipmanv1.AnnotationIpmanName: "test-conn",
						ipmanv1.AnnotationChildName: "child1",
						ipmanv1.AnnotationPoolName:  "pool1",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image",
						},
					},
				},
			},
			existingIPSecConn: createTestIPSecConnection("test-conn", ""),
			expectedAllowed:   true,
			expectedPatchFields: []string{
				"/metadata/annotations/ipman.dialo.ai~1vxlanIp",
				"/metadata/annotations/ipman.dialo.ai~1xfrmIp",
				"/metadata/annotations/ipman.dialo.ai~1interfaceId",
				"/metadata/annotations/ipman.dialo.ai~1remoteIps",
				"/metadata/annotations/ipman.dialo.ai~1localIps",
				"/metadata/annotations/ipman.dialo.ai~1xfrmUnderlyingIp",
				"/spec/initContainers",
				"/spec/containers/0/env",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup the webhook handler
			var wh *MutatingWebhookHandler
			if tt.existingIPSecConn != nil {
				wh = createMockMutatingWebhookHandler(tt.existingIPSecConn)
			} else {
				wh = createMockMutatingWebhookHandler()
			}

			// Create an admission request for the pod
			req := createAdmissionRequest(tt.requestPod, admissionv1.Create)
			req.Kind = metav1.GroupVersionKind{Kind: "Pod"} // Set kind to Pod for testing

			// Create an admission review
			review := createAdmissionReview(req)

			// Send the request to the webhook
			response := testWebhookRequest(t, wh, review)

			// Verify the response
			if response.Response == nil {
				t.Fatalf("Response is nil")
			}

			if response.Response.Allowed != tt.expectedAllowed {
				t.Errorf("Expected allowed=%v, got %v, reason: %s, message: %s", tt.expectedAllowed, response.Response.Allowed, response.Response.Result.Reason, response.Response.Result.Message)
			}

			// Extract and verify patches if the request is allowed
			if tt.expectedAllowed && tt.expectedPatchFields != nil {
				patches := extractPatchOperations(t, response)

				if len(patches) == 0 {
					t.Errorf("Expected patches, got none")
				}

				// Check that each expected field is present in the patches
				for _, field := range tt.expectedPatchFields {
					found := false
					for _, patch := range patches {
						if patch.Path == field {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("Expected patch for field %s, but it was not found", field)
					}
				}

			}
		})
	}
}

func TestCreateInitContainerPatch(t *testing.T) {
	tests := []struct {
		name          string
		pod           *corev1.Pod
		child         ipmanv1.Child
		gateway       string
		ip            string
		image         string
		expectedPath  string
		expectedValue string
	}{
		{
			name: "Pod with no init containers",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{},
				},
			},
			child: ipmanv1.Child{
				Name:      "child1",
				XfrmIfId:  101,
				XfrmIP:    "10.0.0.1/24",
				RemoteIPs: []string{"10.0.1.0/24"},
			},
			gateway:       "10.0.0.2",
			ip:            "10.0.0.3/24",
			image:         "test-image",
			expectedPath:  "/spec/initContainers",
			expectedValue: "array",
		},
		{
			name: "Pod with existing init containers",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name: "existing-init",
						},
					},
				},
			},
			child: ipmanv1.Child{
				Name:      "child1",
				XfrmIfId:  101,
				XfrmIP:    "10.0.0.1/24",
				RemoteIPs: []string{"10.0.1.0/24"},
			},
			gateway:       "10.0.0.2",
			ip:            "10.0.0.3/24",
			image:         "test-image",
			expectedPath:  "/spec/initContainers/-",
			expectedValue: "object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := createInitContainerPatch(tt.pod, tt.child, tt.gateway, tt.ip, tt.image)

			if patch.Path != tt.expectedPath {
				t.Errorf("Expected path %s, got %s", tt.expectedPath, patch.Path)
			}

			// Check if value is of expected type
			switch tt.expectedValue {
			case "array":
				containers, ok := patch.Value.([]corev1.Container)
				if !ok {
					t.Errorf("Expected value to be an array of containers")
				}
				if len(containers) != 1 {
					t.Errorf("Expected 1 container, got %d", len(containers))
				}
				// Verify container has expected properties
				container := containers[0]
				verifyInitContainer(t, container, tt.child, tt.gateway, tt.ip)
			case "object":
				container, ok := patch.Value.(corev1.Container)
				if !ok {
					t.Errorf("Expected value to be a container object")
				}
				// Verify container has expected properties
				verifyInitContainer(t, container, tt.child, tt.gateway, tt.ip)
			}
		})
	}
}

func verifyInitContainer(t *testing.T, container corev1.Container, child ipmanv1.Child, gateway, ip string) {
	if container.Name != ipmanv1.InterfaceRequestContainerName {
		t.Errorf("Expected container name %s, got %s", ipmanv1.InterfaceRequestContainerName, container.Name)
	}

	// Check environment variables
	expectedEnvVars := map[string]string{
		"VXLAN_IP":        ip,
		"XFRM_GATEWAY_IP": gateway,
		"INTERFACE_ID":    "101", // from the child
		"XFRM_IP":         child.XfrmIP,
		"REMOTE_IPS":      "10.0.1.0/24", // from the child
	}

	for _, env := range container.Env {
		if expectedValue, ok := expectedEnvVars[env.Name]; ok {
			if env.Value != expectedValue {
				t.Errorf("Expected env var %s to be %s, got %s", env.Name, expectedValue, env.Value)
			}
		} else {
			t.Errorf("Unexpected env var: %s", env.Name)
		}
	}

	// Check security context
	if container.SecurityContext == nil {
		t.Errorf("Expected security context, got nil")
	} else {
		if container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
			t.Errorf("Expected AllowPrivilegeEscalation to be false")
		}
		if container.SecurityContext.Capabilities == nil {
			t.Errorf("Expected capabilities, got nil")
		} else {
			foundNetAdmin := slices.Contains(container.SecurityContext.Capabilities.Add, "NET_ADMIN")
			if !foundNetAdmin {
				t.Errorf("Expected NET_ADMIN capability, not found")
			}
		}
	}
}

func TestCreateAnnotationPatch(t *testing.T) {
	annotations := map[string]string{
		"key1":            "value1",
		"key2":            "value2",
		"key~1with/slash": "value3", // Test key with slash
	}

	patches := createAnnotationPatch(annotations)

	if len(patches) != 3 {
		t.Errorf("Expected 3 patches, got %d", len(patches))
	}

	// Check each patch
	for _, patch := range patches {
		if patch.Op != "add" {
			t.Errorf("Expected op to be 'add', got '%s'", patch.Op)
		}

		var _, expectedValue string
		switch patch.Path {
		case "/metadata/annotations/key1":
			expectedValue = "value1"
		case "/metadata/annotations/key2":
			expectedValue = "value2"
		case "/metadata/annotations/key~1with~1slash":
			expectedValue = "value3"
		default:
			t.Errorf("Unexpected path: %s", patch.Path)
			continue
		}

		if patch.Value != expectedValue {
			t.Errorf("Expected value %s for path %s, got %s", expectedValue, patch.Path, patch.Value)
		}
	}
}

func TestCreateLabelPatch(t *testing.T) {
	child := ipmanv1.Child{Name: "testchild"}

	patch := createLabelPatch(child)

	if patch.Op != "add" {
		t.Errorf("Expected op to be 'add', got '%s'", patch.Op)
	}

	if patch.Path != "/metadata/labels/ipman.dialo.ai~1worker" {
		t.Errorf("Expected path to be '/metadata/labels/ipman.dialo.ai~1worker', got '%s'", patch.Path)
	}

	if patch.Value != "testchild" {
		t.Errorf("Expected value to be 'testchild', got '%v'", patch.Value)
	}
}

func TestCreateEnvPatch(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		ip             string
		expectedLength int
	}{
		{
			name: "Pod with containers",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "container1"},
						{Name: "container2"},
					},
				},
			},
			ip:             "192.168.1.1/24",
			expectedLength: 2, // One patch per container
		},
		{
			name: "Pod with no containers",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{},
				},
			},
			ip:             "192.168.1.1/24",
			expectedLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := createEnvPatch(tt.pod, tt.ip)

			if (patches == nil && tt.expectedLength > 0) || (patches != nil && len(patches) != tt.expectedLength) {
				t.Errorf("Expected %d patches, got %v", tt.expectedLength, patches)
			}

			if patches != nil {
				for i, patch := range patches {
					if patch.Op != "add" {
						t.Errorf("Patch %d: Expected op to be 'add', got '%s'", i, patch.Op)
					}

					expectedPath := "/spec/containers/" + strconv.FormatInt(int64(i), 10) + "/env"
					if patch.Path != expectedPath {
						t.Errorf("Patch %d: Expected path to be '%s', got '%s'", i, expectedPath, patch.Path)
					}

					env, ok := patch.Value.([]corev1.EnvVar)
					if !ok {
						t.Errorf("Patch %d: Expected value to be of type []corev1.EnvVar", i)
						continue
					}

					if len(env) != 1 {
						t.Errorf("Patch %d: Expected 1 env var, got %d", i, len(env))
						continue
					}

					if env[0].Name != ipmanv1.WorkerContainerVxlanIPEnvVarName {
						t.Errorf("Patch %d: Expected env var name to be '%s', got '%s'", i, ipmanv1.WorkerContainerVxlanIPEnvVarName, env[0].Name)
					}

					if env[0].Value != "192.168.1.1" {
						t.Errorf("Patch %d: Expected env var value to be '192.168.1.1', got '%s'", i, env[0].Value)
					}
				}
			}
		})
	}
}

func TestPatch(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "container1"},
			},
		},
	}

	child := ipmanv1.Child{
		Name:      "child1",
		XfrmIfId:  101,
		XfrmIP:    "10.0.0.1/24",
		RemoteIPs: []string{"10.0.1.0/24"},
	}

	ip := "192.168.1.3/24"
	gateway := "10.0.0.11"
	annotations := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}
	image := "test-image"

	patchBytes := patch(pod, ip, gateway, annotations, child, image)

	if len(patchBytes) == 0 {
		t.Errorf("Expected non-empty patch, got empty")
	}

	// Parse the patch
	var patches []jsonPatch
	err := json.Unmarshal(patchBytes, &patches)
	if err != nil {
		t.Fatalf("Failed to unmarshal patch: %v", err)
	}

	// Count different types of operations
	initContainerPatches := 0
	envPatches := 0
	labelPatches := 0

	for _, p := range patches {
		switch {
		case p.Path == "/spec/initContainers" || p.Path == "/spec/initContainers/-":
			initContainerPatches++
		case p.Path == "/spec/containers/0/env":
			envPatches++
		case p.Path == "/metadata/labels/ipman.dialo.ai~1worker":
			labelPatches++
		}
	}

	if initContainerPatches != 1 {
		t.Errorf("Expected 1 init container patch, got %d", initContainerPatches)
	}

	if envPatches != 1 {
		t.Errorf("Expected 1 env patch, got %d", envPatches)
	}

	if labelPatches != 1 {
		t.Errorf("Expected 1 label patch, got %d", labelPatches)
	}
}
