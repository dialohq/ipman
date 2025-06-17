package controller

import (
	"reflect"
	"testing"

	ipmanv1 "dialo.ai/ipman/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Additional test specifically for the issue mentioned in comments about changes not being detected
func TestDiffXfrmsImageChangeDetection(t *testing.T) {
	// Create a reconciler
	scheme := runtime.NewScheme()
	ipmanv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)

	reconciler := &IPSecConnectionReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
		Env: Envs{
			NamespaceName:            "ipman-system",
			IsTest:                   true,
			WaitForPodTimeoutSeconds: 1,
		},
	}

	// Create desired and current states
	desired := &ClusterState{
		Nodes: []NodeState{
			{
				Xfrms: []IpmanPod[XfrmPodSpec]{
					{
						Meta: PodMeta{
							Name:      "xfrm-image-test",
							Namespace: "ipman-system",
							NodeName:  "test-node",
							Image:     "new-image",
						},
						Spec: XfrmPodSpec{
							Props: XfrmProperties{
								OwnerChild:      "child1",
								OwnerConnection: "conn1",
								InterfaceID:     101,
								XfrmIP:          "10.0.0.1/24",
								VxlanIP:         "10.0.0.2/24",
							},
						},
					},
				},
				NodeName: "test-node",
			},
		},
	}

	current := &ClusterState{
		Nodes: []NodeState{
			{
				Xfrms: []IpmanPod[XfrmPodSpec]{
					{
						Meta: PodMeta{
							Name:      "xfrm-image-test",
							Namespace: "ipman-system",
							NodeName:  "test-node",
							Image:     "old-image",
						},
						Spec: XfrmPodSpec{
							Props: XfrmProperties{
								OwnerChild:      "child1",
								OwnerConnection: "conn1",
								InterfaceID:     101,
								XfrmIP:          "10.0.0.1/24",
								VxlanIP:         "10.0.0.2/24",
							},
						},
					},
				},
				NodeName: "test-node",
			},
		},
	}

	// Store original values to check if they remain unmodified
	desiredImage := desired.Nodes[0].Xfrms[0].Meta.Image
	currentImage := current.Nodes[0].Xfrms[0].Meta.Image

	// Test direct diffXfrms function
	actions := diffXfrms(desired.Nodes[0].Xfrms, current.Nodes[0].Xfrms)

	// Ensure original states are preserved
	if desired.Nodes[0].Xfrms[0].Meta.Image != desiredImage {
		t.Errorf("Desired state was modified: expected image '%s', got '%s'",
			desiredImage, desired.Nodes[0].Xfrms[0].Meta.Image)
	}

	if current.Nodes[0].Xfrms[0].Meta.Image != currentImage {
		t.Errorf("Current state was modified: expected image '%s', got '%s'",
			currentImage, current.Nodes[0].Xfrms[0].Meta.Image)
	}

	// Verify actions are correct
	if len(actions) != 0 {
		t.Errorf("Expected 0 actions (delete+create) for image change, got %d", len(actions))
	}
	// Test through DiffStates
	allActions, err := reconciler.DiffStates(desired, current, []ipmanv1.IPSecConnection{})
	if err != nil {
		t.Fatalf("DiffStates returned error: %v", err)
	}

	// Find Xfrm-related actions
	var xfrmActions []Action
	for _, action := range allActions {
		switch action.(type) {
		case *CreatePodAction[XfrmPodSpec], *DeletePodAction[XfrmPodSpec]:
			xfrmActions = append(xfrmActions, action)
		}
	}

	// Verify actions from DiffStates
	if len(xfrmActions) != 0 {
		t.Errorf("DiffStates: Expected 0 Xfrm actions, got %d", len(xfrmActions))
	}
}

// Test property changes in Xfrm pods
func TestDiffXfrmsPropertyChanges(t *testing.T) {
	// Create original desired and current states with identical properties
	desiredBase := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      "xfrm-properties",
			Namespace: "ipman-system",
			NodeName:  "test-node",
			Image:     "test-image",
		},
		Spec: XfrmPodSpec{
			Props: XfrmProperties{
				OwnerChild:      "child1",
				OwnerConnection: "conn1",
				InterfaceID:     101,
				XfrmIP:          "10.0.0.1/24",
				VxlanIP:         "10.0.0.2/24",
			},
			Routes: Routes{
				Local:  []string{"10.0.0.0/24"},
				Remote: []string{"10.0.1.0/24"},
			},
		},
	}

	currentBase := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      "xfrm-properties",
			Namespace: "ipman-system",
			NodeName:  "test-node",
			Image:     "test-image",
		},
		Spec: XfrmPodSpec{
			Props: XfrmProperties{
				OwnerChild:      "child1",
				OwnerConnection: "conn1",
				InterfaceID:     101,
				XfrmIP:          "10.0.0.1/24",
				VxlanIP:         "10.0.0.2/24",
			},
			Routes: Routes{
				Local:  []string{"10.0.0.0/24"},
				Remote: []string{"10.0.1.0/24"},
			},
		},
	}

	// Test cases for property changes
	tests := []struct {
		name            string
		modifyDesired   func(*IpmanPod[XfrmPodSpec])
		modifyCurrent   func(*IpmanPod[XfrmPodSpec])
		expectedActions int
		expectRecreate  bool // Should generate delete + create
	}{
		{
			name: "Change InterfaceID",
			modifyDesired: func(p *IpmanPod[XfrmPodSpec]) {
				p.Spec.Props.InterfaceID = 102 // Changed from 101
			},
			modifyCurrent:   func(p *IpmanPod[XfrmPodSpec]) {},
			expectedActions: 2,
			expectRecreate:  true,
		},
		{
			name: "Change XfrmIP",
			modifyDesired: func(p *IpmanPod[XfrmPodSpec]) {
				p.Spec.Props.XfrmIP = "10.0.0.3/24" // Changed from 10.0.0.1/24
			},
			modifyCurrent:   func(p *IpmanPod[XfrmPodSpec]) {},
			expectedActions: 2,
			expectRecreate:  true,
		},
		{
			name: "Change VxlanIP",
			modifyDesired: func(p *IpmanPod[XfrmPodSpec]) {
				p.Spec.Props.VxlanIP = "10.0.0.4/24" // Changed from 10.0.0.2/24
			},
			modifyCurrent:   func(p *IpmanPod[XfrmPodSpec]) {},
			expectedActions: 2,
			expectRecreate:  true,
		},
		{
			name: "Change OwnerChild",
			modifyDesired: func(p *IpmanPod[XfrmPodSpec]) {
				p.Spec.Props.OwnerChild = "child2" // Changed from child1
			},
			modifyCurrent:   func(p *IpmanPod[XfrmPodSpec]) {},
			expectedActions: 2,
			expectRecreate:  true,
		},
		{
			name: "Change OwnerConnection",
			modifyDesired: func(p *IpmanPod[XfrmPodSpec]) {
				p.Spec.Props.OwnerConnection = "conn2" // Changed from conn1
			},
			modifyCurrent:   func(p *IpmanPod[XfrmPodSpec]) {},
			expectedActions: 2,
			expectRecreate:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make copies of the base pods
			desired := []IpmanPod[XfrmPodSpec]{desiredBase}
			current := []IpmanPod[XfrmPodSpec]{currentBase}

			// Apply modifications
			tt.modifyDesired(&desired[0])
			tt.modifyCurrent(&current[0])

			// Run diffXfrms
			actions := diffXfrms(desired, current)

			// Verify action count
			if len(actions) != tt.expectedActions {
				t.Errorf("diffXfrms() returned %d actions, expected %d",
					len(actions), tt.expectedActions)
			}

			// Verify action types
			if tt.expectRecreate && len(actions) >= 2 {
				_, okDelete := actions[0].(*DeletePodAction[XfrmPodSpec])
				_, okCreate := actions[1].(*CreatePodAction[XfrmPodSpec])

				if !okDelete || !okCreate {
					t.Errorf("Expected DeletePodAction followed by CreatePodAction, got %T and %T",
						actions[0], actions[1])
				}
			}
		})
	}
}

func TestDiffXfrmsPodIdentification(t *testing.T) {
	// Test how pods are matched between desired and current states

	// Create base pods with different identifying attributes
	pod1 := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      "xfrm-pod-1",
			Namespace: "ipman-system",
			NodeName:  "node1",
			Image:     "test-image",
		},
		Spec: XfrmPodSpec{
			Props: XfrmProperties{
				OwnerChild:      "child1",
				OwnerConnection: "conn1",
				InterfaceID:     101,
				XfrmIP:          "10.0.0.1/24",
				VxlanIP:         "10.0.0.2/24",
			},
		},
	}

	pod2 := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      "xfrm-pod-2",
			Namespace: "ipman-system",
			NodeName:  "node1",
			Image:     "test-image",
		},
		Spec: XfrmPodSpec{
			Props: XfrmProperties{
				OwnerChild:      "child2",
				OwnerConnection: "conn1",
				InterfaceID:     102,
				XfrmIP:          "10.0.0.3/24",
				VxlanIP:         "10.0.0.4/24",
			},
		},
	}

	pod3 := IpmanPod[XfrmPodSpec]{
		Meta: PodMeta{
			Name:      "xfrm-pod-3",
			Namespace: "ipman-system",
			NodeName:  "node1",
			Image:     "test-image",
		},
		Spec: XfrmPodSpec{
			Props: XfrmProperties{
				OwnerChild:      "child3",
				OwnerConnection: "conn1",
				InterfaceID:     103,
				XfrmIP:          "10.0.0.5/24",
				VxlanIP:         "10.0.0.6/24",
			},
		},
	}

	// Test scenarios
	tests := []struct {
		name                  string
		desired               []IpmanPod[XfrmPodSpec]
		current               []IpmanPod[XfrmPodSpec]
		expectedActions       int
		expectSpecificActions func(t *testing.T, actions []Action)
	}{
		{
			name: "Matching pods by name",
			desired: []IpmanPod[XfrmPodSpec]{
				pod1, pod2,
			},
			current: []IpmanPod[XfrmPodSpec]{
				pod1, pod3,
			},
			expectedActions: 2, // Create pod2, Delete pod3
			expectSpecificActions: func(t *testing.T, actions []Action) {
				createFound := false
				deleteFound := false

				for _, action := range actions {
					switch a := action.(type) {
					case *CreatePodAction[XfrmPodSpec]:
						if a.Pod.Meta.Name == "xfrm-pod-2" {
							createFound = true
						}
					case *DeletePodAction[XfrmPodSpec]:
						if a.Pod.Meta.Name == "xfrm-pod-3" {
							deleteFound = true
						}
					}
				}

				if !createFound {
					t.Errorf("Expected create action for pod 'xfrm-pod-2'")
				}
				if !deleteFound {
					t.Errorf("Expected delete action for pod 'xfrm-pod-3'")
				}
			},
		},
		{
			name: "Same names but different properties",
			desired: []IpmanPod[XfrmPodSpec]{
				{
					Meta: pod1.Meta,
					Spec: XfrmPodSpec{
						Props: XfrmProperties{
							OwnerChild:      "child1-new", // Changed
							OwnerConnection: "conn1",
							InterfaceID:     101,
							XfrmIP:          "10.0.0.1/24",
							VxlanIP:         "10.0.0.2/24",
						},
					},
				},
			},
			current: []IpmanPod[XfrmPodSpec]{
				pod1,
			},
			expectedActions: 2, // Delete and recreate pod1 with new properties
			expectSpecificActions: func(t *testing.T, actions []Action) {
				if len(actions) != 2 {
					t.Fatalf("Expected 2 actions, got %d", len(actions))
				}

				deleteAction, okDelete := actions[0].(*DeletePodAction[XfrmPodSpec])
				createAction, okCreate := actions[1].(*CreatePodAction[XfrmPodSpec])

				if !okDelete || !okCreate {
					t.Errorf("Expected DeletePodAction followed by CreatePodAction, got %T and %T",
						actions[0], actions[1])
				} else {
					// Check pod name is the same
					if deleteAction.Pod.Meta.Name != "xfrm-pod-1" || createAction.Pod.Meta.Name != "xfrm-pod-1" {
						t.Errorf("Expected actions for pod 'xfrm-pod-1', got delete:'%s', create:'%s'",
							deleteAction.Pod.Meta.Name, createAction.Pod.Meta.Name)
					}

					// Check properties differ
					if deleteAction.Pod.Spec.Props.OwnerChild != "child1" {
						t.Errorf("Expected deleted pod to have OwnerChild='child1', got '%s'",
							deleteAction.Pod.Spec.Props.OwnerChild)
					}

					if createAction.Pod.Spec.Props.OwnerChild != "child1-new" {
						t.Errorf("Expected created pod to have OwnerChild='child1-new', got '%s'",
							createAction.Pod.Spec.Props.OwnerChild)
					}
				}
			},
		},
		{
			name: "Multiple pods with various changes",
			desired: []IpmanPod[XfrmPodSpec]{
				pod1, // unchanged
				{
					Meta: pod2.Meta,
					Spec: XfrmPodSpec{
						Props: XfrmProperties{
							OwnerChild:      pod2.Spec.Props.OwnerChild,
							OwnerConnection: pod2.Spec.Props.OwnerConnection,
							InterfaceID:     202, // Changed from 102
							XfrmIP:          pod2.Spec.Props.XfrmIP,
							VxlanIP:         pod2.Spec.Props.VxlanIP,
						},
					},
				},
				{
					Meta: PodMeta{ // New pod
						Name:      "xfrm-pod-4",
						Namespace: "ipman-system",
						NodeName:  "node1",
						Image:     "test-image",
					},
					Spec: XfrmPodSpec{
						Props: XfrmProperties{
							OwnerChild:      "child4",
							OwnerConnection: "conn1",
							InterfaceID:     104,
							XfrmIP:          "10.0.0.7/24",
							VxlanIP:         "10.0.0.8/24",
						},
					},
				},
			},
			current: []IpmanPod[XfrmPodSpec]{
				pod1, // unchanged
				pod2, // will be changed
				pod3, // will be deleted
			},
			expectedActions: 4, // Delete pod2, Create pod2 with new props, Delete pod3, Create pod4
			expectSpecificActions: func(t *testing.T, actions []Action) {
				if len(actions) != 4 {
					t.Fatalf("Expected 4 actions, got %d", len(actions))
				}

				deleteCount := 0
				createCount := 0
				deleteNames := make(map[string]bool)
				createNames := make(map[string]bool)

				for _, action := range actions {
					switch a := action.(type) {
					case *DeletePodAction[XfrmPodSpec]:
						deleteCount++
						deleteNames[a.Pod.Meta.Name] = true
					case *CreatePodAction[XfrmPodSpec]:
						createCount++
						createNames[a.Pod.Meta.Name] = true
					}
				}

				if deleteCount != 2 {
					t.Errorf("Expected 2 delete actions, got %d", deleteCount)
				}
				if createCount != 2 {
					t.Errorf("Expected 2 create actions, got %d", createCount)
				}

				if !deleteNames["xfrm-pod-2"] || !deleteNames["xfrm-pod-3"] {
					t.Errorf("Expected deletes for pod2 and pod3, got %v", deleteNames)
				}
				if !createNames["xfrm-pod-2"] || !createNames["xfrm-pod-4"] {
					t.Errorf("Expected creates for pod2 and pod4, got %v", createNames)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := diffXfrms(tt.desired, tt.current)

			if len(actions) != tt.expectedActions {
				t.Errorf("diffXfrms() returned %d actions, expected %d",
					len(actions), tt.expectedActions)
			}

			if tt.expectSpecificActions != nil {
				tt.expectSpecificActions(t, actions)
			}
		})
	}
}

func TestDiffXfrmsDeepCopying(t *testing.T) {
	// Test that diffXfrms properly copies objects and doesn't modify the originals

	// Create original states
	originalDesired := []IpmanPod[XfrmPodSpec]{
		{
			Meta: PodMeta{
				Name:      "xfrm-test",
				Namespace: "ipman-system",
				NodeName:  "test-node",
				Image:     "new-image",
			},
			Spec: XfrmPodSpec{
				Props: XfrmProperties{
					OwnerChild:      "child1",
					OwnerConnection: "conn1",
					InterfaceID:     101,
					XfrmIP:          "10.0.0.1/24",
					VxlanIP:         "10.0.0.2/24",
				},
				Routes: Routes{
					Local:  []string{"10.0.0.0/24"},
					Remote: []string{"10.0.1.0/24"},
				},
			},
		},
	}

	originalCurrent := []IpmanPod[XfrmPodSpec]{
		{
			Meta: PodMeta{
				Name:      "xfrm-test",
				Namespace: "ipman-system",
				NodeName:  "test-node",
				Image:     "old-image",
			},
			Spec: XfrmPodSpec{
				Props: XfrmProperties{
					OwnerChild:      "child1",
					OwnerConnection: "conn1",
					InterfaceID:     101,
					XfrmIP:          "10.0.0.1/24",
					VxlanIP:         "10.0.0.2/24",
				},
				Routes: Routes{
					Local:  []string{"10.0.0.0/24"},
					Remote: []string{"10.0.1.0/24"},
				},
			},
		},
	}

	// Create copies to verify against later
	desiredCopy := make([]IpmanPod[XfrmPodSpec], len(originalDesired))
	copy(desiredCopy, originalDesired)

	currentCopy := make([]IpmanPod[XfrmPodSpec], len(originalCurrent))
	copy(currentCopy, originalCurrent)

	// Run diffXfrms
	_ = diffXfrms(originalDesired, originalCurrent)

	// Verify that the originals were not modified
	if !reflect.DeepEqual(originalDesired, desiredCopy) {
		t.Errorf("diffXfrms modified the original desired state")
	}

	if !reflect.DeepEqual(originalCurrent, currentCopy) {
		t.Errorf("diffXfrms modified the original current state")
	}
}
