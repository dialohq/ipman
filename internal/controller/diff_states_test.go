package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"github.com/r3labs/diff/v3"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// createTestReconciler creates a reconciler for testing
func createTestReconciler() *IPSecConnectionReconciler {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ipmanv1.AddToScheme(scheme)

	return &IPSecConnectionReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
		Env: Envs{
			NamespaceName:            "ipman-system",
			IsTest:                   true,
			WaitForPodTimeoutSeconds: 1,
		},
	}
}

// TestDiffStatesComprehensive tests the DiffStates function more comprehensively
func TestDiffStatesComprehensive(t *testing.T) {
	// Helper function to create a basic NodeState
	createBasicNodeState := func() GroupState {
		return GroupState{
			Charon: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			Proxy: &IpmanPod[RestctlPodSpec]{
				Meta: PodMeta{
					Name:      "restctl-pod-test",
					Namespace: "ipman-system",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: RestctlPodSpec{
					HostPath: "/test/path",
				},
			},
			Xfrms: []IpmanPod[XfrmPodSpec]{
				{
					Meta: PodMeta{
						Name:      "xfrm-pod-test",
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
						Routes: Routes{},
					},
				},
			},
		}
	}

	tests := []struct {
		name            string
		setupDesired    func(ns GroupState) GroupState
		setupCurrent    func(ns GroupState) GroupState
		expectedActions int
		validateActions func(t *testing.T, actions []Action)
	}{
		{
			name: "Identical states",
			setupDesired: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			setupCurrent: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			expectedActions: 0,
			validateActions: nil,
		},
		{
			name: "All pods missing in current state",
			setupDesired: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			setupCurrent: func(ns GroupState) GroupState {
				return GroupState{
					Charon: nil,
					Proxy:  nil,
					Xfrms:  []IpmanPod[XfrmPodSpec]{},
				}
			},
			expectedActions: 4, // Create actions for Charon, Proxy, 1 Xfrm and override config
			validateActions: func(t *testing.T, actions []Action) {
				for _, action := range actions {
					if _, ok := action.(*CreatePodAction[CharonPodSpec]); ok {
						continue
					}
					if _, ok := action.(*CreatePodAction[RestctlPodSpec]); ok {
						continue
					}
					if _, ok := action.(*CreatePodAction[XfrmPodSpec]); ok {
						continue
					}
					if _, ok := action.(*OverrideConfigAction); ok {
						continue
					}
					t.Errorf("Unexpected action type: %T", action)
				}
			},
		},
		{
			name: "All pods missing in desired state",
			setupDesired: func(ns GroupState) GroupState {
				return GroupState{
					Charon: nil,
					Proxy:  nil,
					Xfrms:  []IpmanPod[XfrmPodSpec]{},
				}
			},
			setupCurrent: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			expectedActions: 3, // Delete actions for Charon, Proxy and 1 xfrm
			validateActions: func(t *testing.T, actions []Action) {
				for _, action := range actions {
					if _, ok := action.(*DeletePodAction[CharonPodSpec]); ok {
						continue
					}
					if _, ok := action.(*DeletePodAction[RestctlPodSpec]); ok {
						continue
					}
					if _, ok := action.(*DeletePodAction[XfrmPodSpec]); ok {
						continue
					}
					t.Errorf("Unexpected action type: %T", action)
				}
			},
		},
		{
			name: "Charon pod image changed",
			setupDesired: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				ns2.Charon.Meta.Image = "new-image"
				return *ns2
			},
			setupCurrent: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			expectedActions: 0, // Don't auto recreate critical pods
			validateActions: func(t *testing.T, actions []Action) {

				if len(actions) != 0 {
					actionNames := []string{}
					for _, a := range actions {
						actionNames = append(actionNames, reflect.TypeOf(a).String())
					}
					t.Fatalf("Expected 0 actions, got %d, %+v", len(actions), actionNames)
				}
			},
		},
		{
			name: "All pod specs changed",
			setupDesired: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				ns2.Charon.Meta.Image = "new-charon-image"
				ns2.Proxy.Meta.Image = "new-proxy-image"
				ns2.Charon.Spec.HostPath = "/new/path"
				return *ns2
			},
			setupCurrent: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			expectedActions: 0, // Don't destroy critical pods
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 0 {
					t.Errorf("Expected 0 actions found %d", len(actions))
				}
			},
		},
		{
			name: "Node changed for all pods",
			setupDesired: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				ns2.Charon.Meta.NodeName = "new-node"
				ns2.Proxy.Meta.NodeName = "new-node"
				for i := range ns2.Xfrms {
					ns2.Xfrms[i].Meta.NodeName = "new-node"
				}
				return *ns2
			},
			setupCurrent: func(ns GroupState) GroupState {
				out, _ := json.Marshal(ns)
				ns2 := &GroupState{}
				_ = json.Unmarshal(out, ns2)
				return *ns2
			},
			expectedActions: 7, // Delete and create for Charon, Proxy, override config, and Xfrm
			validateActions: func(t *testing.T, actions []Action) {
				deleteCount := 0
				createCount := 0
				overrideCount := 0

				for _, action := range actions {
					switch action.(type) {
					case *DeletePodAction[CharonPodSpec], *DeletePodAction[RestctlPodSpec], *DeletePodAction[XfrmPodSpec]:
						deleteCount++
						// Check that the node is the old one
						switch typedAction := action.(type) {
						case *DeletePodAction[CharonPodSpec]:
							if typedAction.Pod.Meta.NodeName != "test-node" {
								t.Errorf("Expected deleted pod node to be 'test-node', got '%s'", typedAction.Pod.Meta.NodeName)
							}
						case *DeletePodAction[RestctlPodSpec]:
							if typedAction.Pod.Meta.NodeName != "test-node" {
								t.Errorf("Expected deleted pod node to be 'test-node', got '%s'", typedAction.Pod.Meta.NodeName)
							}
						case *DeletePodAction[XfrmPodSpec]:
							if typedAction.Pod.Meta.NodeName != "test-node" {
								t.Errorf("Expected deleted pod node to be 'test-node', got '%s'", typedAction.Pod.Meta.NodeName)
							}
						}
					case *CreatePodAction[CharonPodSpec], *CreatePodAction[RestctlPodSpec], *CreatePodAction[XfrmPodSpec]:
						createCount++
						// Check that the node is the new one
						switch typedAction := action.(type) {
						case *CreatePodAction[CharonPodSpec]:
							if typedAction.Pod.Meta.NodeName != "new-node" {
								t.Errorf("Expected created pod node to be 'new-node', got '%s'", typedAction.Pod.Meta.NodeName)
							}
						case *CreatePodAction[RestctlPodSpec]:
							if typedAction.Pod.Meta.NodeName != "new-node" {
								t.Errorf("Expected created pod node to be 'new-node', got '%s'", typedAction.Pod.Meta.NodeName)
							}
						case *CreatePodAction[XfrmPodSpec]:
							if typedAction.Pod.Meta.NodeName != "new-node" {
								t.Errorf("Expected created pod node to be 'new-node', got '%s'", typedAction.Pod.Meta.NodeName)
							}
						}
					case *OverrideConfigAction:
						overrideCount++
					default:
						t.Errorf("Unexpected action type: %T", action)
					}
				}

				if deleteCount != 3 {
					t.Errorf("Expected 3 delete actions, got %d", deleteCount)
				}
				if createCount != 3 {
					t.Errorf("Expected 3 create actions, got %d", createCount)
				}
				if overrideCount != 1 {
					t.Errorf("Expected 1 override action, got %d", overrideCount)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseNodeState := createBasicNodeState()

			desiredState := &ClusterState{
				Groups: []GroupState{
					tt.setupDesired(baseNodeState),
				},
			}

			currentState := &ClusterState{
				Groups: []GroupState{
					tt.setupCurrent(baseNodeState),
				},
			}

			r := createTestReconciler()
			actions, err := r.DiffStates(desiredState, currentState, []ipmanv1.IPSecConnection{})
			if err != nil {
				t.Fatalf("DiffStates() returned an error: %v", err)
			}

			if actions == nil && tt.expectedActions > 0 {
				t.Fatalf("DiffStates() returned nil, expected %d actions", tt.expectedActions)
			}

			if actions != nil {
				if len(actions) != tt.expectedActions {
					t.Errorf("DiffStates() returned %d actions, expected %d", len(actions), tt.expectedActions)
				}

				if tt.validateActions != nil {
					tt.validateActions(t, actions)
				}
			}
		})
	}
}

// TestDiffStatesWithMultipleNodes tests DiffStates with multiple nodes
func TestDiffStatesWithMultipleNodes(t *testing.T) {
	node1 := GroupState{
		Charon: &IpmanPod[CharonPodSpec]{
			Meta: PodMeta{
				Name:      "charon-pod-node1",
				Namespace: "ipman-system",
				NodeName:  "node1",
				Image:     "charon-image",
			},
			Spec: CharonPodSpec{
				HostPath: "/node1/path",
			},
		},
		Proxy:    nil,
		Xfrms:    []IpmanPod[XfrmPodSpec]{},
		NodeName: "localcluster",
	}

	node2 := GroupState{
		Charon: &IpmanPod[CharonPodSpec]{
			Meta: PodMeta{
				Name:      "charon-pod-node2",
				Namespace: "ipman-system",
				NodeName:  "node2",
				Image:     "charon-image",
			},
			Spec: CharonPodSpec{
				HostPath: "/node2/path",
			},
		},
		Proxy: &IpmanPod[RestctlPodSpec]{
			Meta: PodMeta{
				Name:      "restctl-pod-node2",
				Namespace: "ipman-system",
				NodeName:  "node2",
				Image:     "proxy-image",
			},
			Spec: RestctlPodSpec{},
		},
		Xfrms: []IpmanPod[XfrmPodSpec]{
			{
				Meta: PodMeta{
					Name:      "xfrm-pod-node2",
					Namespace: "ipman-system",
					NodeName:  "node2",
					Image:     "xfrm-image",
				},
				Spec: XfrmPodSpec{
					Props: XfrmProperties{
						OwnerChild:      "child2",
						OwnerConnection: "conn2",
						InterfaceID:     102,
						XfrmIP:          "10.1.0.1/24",
						VxlanIP:         "10.1.0.2/24",
					},
					Routes: Routes{},
				},
			},
		},
		NodeName: "localcluster2",
	}

	out, _ := json.Marshal(node1)
	changed_node1 := GroupState{}
	_ = json.Unmarshal(out, &changed_node1)
	changed_node1.Charon.Meta.Image = "new-charon-image"
	fmt.Println(string(out))

	out, _ = json.Marshal(node2)
	changed_node2 := GroupState{}
	_ = json.Unmarshal(out, &changed_node2)
	changed_node2.Proxy.Meta.Image = "new-proxy-image"
	fmt.Println(string(out))

	out, _ = json.Marshal(node1)
	node_with_pods1 := GroupState{}
	_ = json.Unmarshal(out, &node_with_pods1)
	fmt.Println(string(out))
	node_with_pods1.Proxy = &IpmanPod[RestctlPodSpec]{
		Meta: PodMeta{
			Name:      "restctl-pod-node1",
			Namespace: "ipman-system",
			NodeName:  "node1",
			Image:     "proxy-image",
		},
		Spec: RestctlPodSpec{},
	}

	out, _ = json.Marshal(node2)
	node_with_pods2 := GroupState{}
	_ = json.Unmarshal(out, &node_with_pods2)
	fmt.Println(string(out))
	fmt.Println(string(out))

	fmt.Println("starting tests")
	tests := []struct {
		name            string
		desiredSetup    ClusterState
		currentSetup    ClusterState
		expectedActions int
	}{
		{
			name:            "Identical multi-node state",
			desiredSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			currentSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			expectedActions: 0,
		},
		{
			name:            "Change only on node1",
			desiredSetup:    ClusterState{Groups: []GroupState{changed_node1, node2}},
			currentSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			expectedActions: 0,
		},
		{
			name:            "Change only on node2",
			desiredSetup:    ClusterState{Groups: []GroupState{node1, changed_node2}},
			currentSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			expectedActions: 0,
		},
		{
			name:            "Changes on both nodes",
			desiredSetup:    ClusterState{Groups: []GroupState{changed_node1, changed_node2}},
			currentSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			expectedActions: 0, // Delete and create for both nodes
		},
		{
			name:            "Add missing pods on both nodes",
			desiredSetup:    ClusterState{Groups: []GroupState{node_with_pods1, node_with_pods2}},
			currentSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			expectedActions: 2, // Create for node1 Proxy, and override config
		},
		{
			name:            "Remove pods from both nodes",
			desiredSetup:    ClusterState{Groups: []GroupState{node1, node2}},
			currentSetup:    ClusterState{Groups: []GroupState{node_with_pods1, node_with_pods2}},
			expectedActions: 1, // Delete for node1 Proxy
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("------------\n%+v\n%+v\n", tt.currentSetup, tt.desiredSetup)
			r := createTestReconciler()
			actions, err := r.DiffStates(&tt.desiredSetup, &tt.currentSetup, []ipmanv1.IPSecConnection{})
			if err != nil {
				t.Fatalf("DiffStates() returned an error: %v", err)
			}

			if actions == nil && tt.expectedActions > 0 {
				t.Fatalf("DiffStates() returned nil, expected %d actions", tt.expectedActions)
			}

			if actions != nil && len(actions) != tt.expectedActions {
				t.Errorf("DiffStates() returned %d actions, expected %d", len(actions), tt.expectedActions)
			}
		})
	}
}

// TestChangeFunctions tests the utility functions IsNodeChanged, isCreated, and isDeleted
func TestChangeFunctions(t *testing.T) {
	tests := []struct {
		name             string
		change           diff.Change
		expectNodeChange bool
		expectCreated    bool
		expectDeleted    bool
	}{
		{
			name: "Node change",
			change: diff.Change{
				Type: diff.UPDATE,
				Path: []string{"meta", "node"},
				From: "old-node",
				To:   "new-node",
			},
			expectNodeChange: true,
			expectCreated:    false,
			expectDeleted:    false,
		},
		{
			name: "Create change",
			change: diff.Change{
				Type: diff.CREATE,
				Path: []string{},
				From: nil,
				To:   "something",
			},
			expectNodeChange: false,
			expectCreated:    true,
			expectDeleted:    false,
		},
		{
			name: "Delete change",
			change: diff.Change{
				Type: diff.DELETE,
				Path: []string{},
				From: "something",
				To:   nil,
			},
			expectNodeChange: false,
			expectCreated:    false,
			expectDeleted:    true,
		},
		{
			name: "Image change",
			change: diff.Change{
				Type: diff.UPDATE,
				Path: []string{"meta", "image"},
				From: "old-image",
				To:   "new-image",
			},
			expectNodeChange: false,
			expectCreated:    false,
			expectDeleted:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isNode := IsNodeChanged(tt.change)
			created := isCreated(tt.change)
			deleted := isDeleted(tt.change)

			if isNode != tt.expectNodeChange {
				t.Errorf("IsNodeChanged() = %v, want %v", isNode, tt.expectNodeChange)
			}
			if created != tt.expectCreated {
				t.Errorf("isCreated() = %v, want %v", created, tt.expectCreated)
			}
			if deleted != tt.expectDeleted {
				t.Errorf("isDeleted() = %v, want %v", deleted, tt.expectDeleted)
			}
		})
	}
}

// TestDiffImmutablePod tests the diffImmutablePod function with various scenarios
func TestDiffImmutablePodExtensive(t *testing.T) {
	tests := []struct {
		name            string
		desired         *IpmanPod[CharonPodSpec]
		current         *IpmanPod[CharonPodSpec]
		expectedActions int
		expectedTypes   []string // Array of expected action type names
	}{
		{
			name:            "Both nil",
			desired:         nil,
			current:         nil,
			expectedActions: 0,
			expectedTypes:   []string{},
		},
		{
			name: "Identical pods",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expectedActions: 0,
			expectedTypes:   []string{},
		},
		{
			name:    "Desired pod missing (delete)",
			desired: nil,
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expectedActions: 1,
			expectedTypes:   []string{"*controller.DeletePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]"},
		},
		{
			name: "Current pod missing (create)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current:         nil,
			expectedActions: 1,
			expectedTypes:   []string{"*controller.CreatePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]"},
		},
		{
			name: "Different image",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					NodeName:  "test-node",
					Image:     "new-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					NodeName:  "test-node",
					Image:     "old-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expectedActions: 0,
			expectedTypes: []string{
				"*controller.DeletePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]",
				"*controller.CreatePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]",
			},
		},
		{
			name: "Different namespace",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "new-namespace",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "old-namespace",
					NodeName:  "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expectedActions: 2,
			expectedTypes: []string{
				"*controller.DeletePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]",
				"*controller.CreatePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]",
			},
		},
		{
			name: "Multiple differences (recreate)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "new-namespace",
					NodeName:  "new-node",
					Image:     "new-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/new/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "old-namespace",
					NodeName:  "old-node",
					Image:     "old-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/old/path",
				},
			},
			expectedActions: 2,
			expectedTypes: []string{
				"*controller.DeletePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]",
				"*controller.CreatePodAction[dialo.ai/ipman/internal/controller.CharonPodSpec]",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := diffImmutablePod(tt.desired, tt.current)

			if len(actions) != tt.expectedActions {
				t.Errorf("diffImmutablePod() returned %d actions, expected %d", len(actions), tt.expectedActions)
			}

			// Check that the returned action types match what we expect
			if len(actions) > 0 {
				for i, action := range actions {
					if i >= len(tt.expectedTypes) {
						t.Errorf("Unexpected action: %T", action)
						continue
					}

					actionType := reflect.TypeOf(action).String()
					if actionType != tt.expectedTypes[i] {
						t.Errorf("Action %d: expected type %s, got %s", i, tt.expectedTypes[i], actionType)
					}
				}
			}
		})
	}
}
