package controller

import (
	"testing"

	"github.com/r3labs/diff/v3"
)

func TestDiffCharon(t *testing.T) {
	tests := []struct {
		name     string
		desired  *IpmanPod[CharonPodSpec]
		current  *IpmanPod[CharonPodSpec]
		expected int // Expected number of actions
	}{
		{
			name:     "Both nil",
			desired:  nil,
			current:  nil,
			expected: 0,
		},
		{
			name: "Identical pods",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 0,
		},
		{
			name:    "Desired pod missing (delete)",
			desired: nil,
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 1, // Should generate a delete action
		},
		{
			name: "Current pod missing (create)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current:  nil,
			expected: 1, // Should generate a create action
		},
		{
			name: "Different image (recreate)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "new-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "old-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 2, // Should generate delete and create actions
		},
		{
			name: "Different hostPath (recreate)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/new/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/old/path",
				},
			},
			expected: 2, // Should generate delete and create actions
		},
		{
			name: "Different node (recreate)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "new-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "charon-pod-test",
					Namespace: "ipman-system",
					Node:      "old-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 2, // Should generate delete and create actions
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := diffCharon(tt.desired, tt.current)
			if len(actions) != tt.expected {
				t.Errorf("diffCharon() returned %d actions, expected %d", len(actions), tt.expected)
			}

			// Check the types of actions returned
			if tt.expected > 0 {
				if tt.desired == nil && tt.current != nil {
					// Should be a delete action
					if _, ok := actions[0].(*DeletePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected DeletePodAction, got %T", actions[0])
					}
				} else if tt.desired != nil && tt.current == nil {
					// Should be a create action
					if _, ok := actions[0].(*CreatePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected CreatePodAction, got %T", actions[0])
					}
				} else if tt.desired != nil && tt.current != nil && tt.expected == 2 {
					// Should be delete then create
					if _, ok := actions[0].(*DeletePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected first action to be DeletePodAction, got %T", actions[0])
					}
					if _, ok := actions[1].(*CreatePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected second action to be CreatePodAction, got %T", actions[1])
					}
				}
			}
		})
	}
}

func TestDiffProxy(t *testing.T) {
	tests := []struct {
		name     string
		desired  *IpmanPod[ProxyPodSpec]
		current  *IpmanPod[ProxyPodSpec]
		expected int // Expected number of actions
	}{
		{
			name:     "Both nil",
			desired:  nil,
			current:  nil,
			expected: 0,
		},
		{
			name: "Identical pods",
			desired: &IpmanPod[ProxyPodSpec]{
				Meta: PodMeta{
					Name:      "proxy-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: ProxyPodSpec{},
			},
			current: &IpmanPod[ProxyPodSpec]{
				Meta: PodMeta{
					Name:      "proxy-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: ProxyPodSpec{},
			},
			expected: 0,
		},
		{
			name:    "Desired pod missing (delete)",
			desired: nil,
			current: &IpmanPod[ProxyPodSpec]{
				Meta: PodMeta{
					Name:      "proxy-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: ProxyPodSpec{},
			},
			expected: 1, // Should generate a delete action
		},
		{
			name: "Current pod missing (create)",
			desired: &IpmanPod[ProxyPodSpec]{
				Meta: PodMeta{
					Name:      "proxy-pod-test",
					Namespace: "ipman-system",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: ProxyPodSpec{},
			},
			current:  nil,
			expected: 1, // Should generate a create action
		},
		{
			name: "Different node (recreate)",
			desired: &IpmanPod[ProxyPodSpec]{
				Meta: PodMeta{
					Name:      "proxy-pod-test",
					Namespace: "ipman-system",
					Node:      "new-node",
					Image:     "test-image",
				},
				Spec: ProxyPodSpec{},
			},
			current: &IpmanPod[ProxyPodSpec]{
				Meta: PodMeta{
					Name:      "proxy-pod-test",
					Namespace: "ipman-system",
					Node:      "old-node",
					Image:     "test-image",
				},
				Spec: ProxyPodSpec{},
			},
			expected: 2, // Should generate delete and create actions
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := diffProxy(tt.desired, tt.current)
			if len(actions) != tt.expected {
				t.Errorf("diffProxy() returned %d actions, expected %d", len(actions), tt.expected)
			}

			// Check the types of actions returned
			if tt.expected > 0 {
				if tt.desired == nil && tt.current != nil {
					// Should be a delete action
					if _, ok := actions[0].(*DeletePodAction[ProxyPodSpec]); !ok {
						t.Errorf("Expected DeletePodAction, got %T", actions[0])
					}
				} else if tt.desired != nil && tt.current == nil {
					// Should be a create action
					if _, ok := actions[0].(*CreatePodAction[ProxyPodSpec]); !ok {
						t.Errorf("Expected CreatePodAction, got %T", actions[0])
					}
				} else if tt.desired != nil && tt.current != nil && tt.expected == 2 {
					// Should be delete then create
					if _, ok := actions[0].(*DeletePodAction[ProxyPodSpec]); !ok {
						t.Errorf("Expected first action to be DeletePodAction, got %T", actions[0])
					}
					if _, ok := actions[1].(*CreatePodAction[ProxyPodSpec]); !ok {
						t.Errorf("Expected second action to be CreatePodAction, got %T", actions[1])
					}
				}
			}
		})
	}
}

func TestDiffImmutablePod(t *testing.T) {
	tests := []struct {
		name     string
		desired  *IpmanPod[CharonPodSpec]
		current  *IpmanPod[CharonPodSpec]
		expected int // Expected number of actions
	}{
		{
			name:     "Both nil",
			desired:  nil,
			current:  nil,
			expected: 0,
		},
		{
			name: "Identical pods",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Node:      "test-node",
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
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 0,
		},
		{
			name:    "Desired pod missing (delete)",
			desired: nil,
			current: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 1, // Should generate a delete action
		},
		{
			name: "Current pod missing (create)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Node:      "test-node",
					Image:     "test-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			current:  nil,
			expected: 1, // Should generate a create action
		},
		{
			name: "Different pods (recreate)",
			desired: &IpmanPod[CharonPodSpec]{
				Meta: PodMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Node:      "test-node",
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
					Node:      "test-node",
					Image:     "old-image",
				},
				Spec: CharonPodSpec{
					HostPath: "/test/path",
				},
			},
			expected: 2, // Should generate delete and create actions
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := diffImmutablePod(tt.desired, tt.current)
			if len(actions) != tt.expected {
				t.Errorf("diffImmutablePod() returned %d actions, expected %d", len(actions), tt.expected)
			}

			// Check the types of actions returned
			if tt.expected > 0 {
				if tt.desired == nil && tt.current != nil {
					// Should be a delete action
					if _, ok := actions[0].(*DeletePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected DeletePodAction, got %T", actions[0])
					}
				} else if tt.desired != nil && tt.current == nil {
					// Should be a create action
					if _, ok := actions[0].(*CreatePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected CreatePodAction, got %T", actions[0])
					}
				} else if tt.desired != nil && tt.current != nil && tt.expected == 2 {
					// Should be delete then create
					if _, ok := actions[0].(*DeletePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected first action to be DeletePodAction, got %T", actions[0])
					}
					if _, ok := actions[1].(*CreatePodAction[CharonPodSpec]); !ok {
						t.Errorf("Expected second action to be CreatePodAction, got %T", actions[1])
					}
				}
			}
		})
	}
}

// TestIsCreatedAndIsDeleted tests the utility functions for change detection
func TestIsCreatedAndIsDeleted(t *testing.T) {
	tests := []struct {
		name            string
		change          diff.Change
		expectedCreated bool
		expectedDeleted bool
	}{
		{
			name: "Created change",
			change: diff.Change{
				Type: diff.CREATE,
				Path: []string{},
				From: nil,
				To:   "something",
			},
			expectedCreated: true,
			expectedDeleted: false,
		},
		{
			name: "Deleted change",
			change: diff.Change{
				Type: diff.DELETE,
				Path: []string{},
				From: "something",
				To:   nil,
			},
			expectedCreated: false,
			expectedDeleted: true,
		},
		{
			name: "Modified change",
			change: diff.Change{
				Type: diff.UPDATE,
				Path: []string{"meta", "image"},
				From: "old-image",
				To:   "new-image",
			},
			expectedCreated: false,
			expectedDeleted: false,
		},
		{
			name: "Nested path change",
			change: diff.Change{
				Type: diff.CREATE,
				Path: []string{"spec", "hostPath"},
				From: nil,
				To:   "/new/path",
			},
			expectedCreated: false,
			expectedDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := isCreated(tt.change)
			if created != tt.expectedCreated {
				t.Errorf("isCreated() = %v, want %v", created, tt.expectedCreated)
			}

			deleted := isDeleted(tt.change)
			if deleted != tt.expectedDeleted {
				t.Errorf("isDeleted() = %v, want %v", deleted, tt.expectedDeleted)
			}
		})
	}
}

// TestIsNodeChanged tests the IsNodeChanged function
func TestIsNodeChanged(t *testing.T) {
	tests := []struct {
		name     string
		change   diff.Change
		expected bool
	}{
		{
			name: "Node change",
			change: diff.Change{
				Type: diff.UPDATE,
				Path: []string{"meta", "node"},
				From: "old-node",
				To:   "new-node",
			},
			expected: true,
		},
		{
			name: "Image change",
			change: diff.Change{
				Type: diff.UPDATE,
				Path: []string{"meta", "image"},
				From: "old-image",
				To:   "new-image",
			},
			expected: false,
		},
		{
			name: "Empty path",
			change: diff.Change{
				Type: diff.CREATE,
				Path: []string{},
				From: nil,
				To:   "something",
			},
			expected: false,
		},
		{
			name: "Path with different length",
			change: diff.Change{
				Type: diff.UPDATE,
				Path: []string{"meta", "node", "extra"},
				From: "old-value",
				To:   "new-value",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNodeChanged(tt.change)
			if result != tt.expected {
				t.Errorf("IsNodeChanged() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestDiffStatesForPodChanges tests DiffStates function specifically for pod differences
func TestDiffStatesForPodChanges(t *testing.T) {
	tests := []struct {
		name            string
		desiredState    func() *ClusterState // Use factory functions to create fresh states for each test
		currentState    func() *ClusterState
		expectedActions int
	}{
		{
			name: "Identical states",
			desiredState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "test-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: nil,
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			currentState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "test-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: nil,
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			expectedActions: 0,
		},
		{
			name: "Missing Charon pod in current",
			desiredState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "test-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: nil,
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			currentState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: nil,
							Proxy:  nil,
							Xfrms:  []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			expectedActions: 1, // Create Charon pod
		},
		{
			name: "Different Charon image",
			desiredState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "new-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: nil,
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			currentState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "old-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: nil,
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			expectedActions: 2, // Delete and create Charon pod
		},
		{
			name: "Missing Proxy pod in current",
			desiredState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: nil,
							Proxy: &IpmanPod[ProxyPodSpec]{
								Meta: PodMeta{
									Name:      "proxy-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "test-image",
								},
								Spec: ProxyPodSpec{},
							},
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			currentState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: nil,
							Proxy:  nil,
							Xfrms:  []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			expectedActions: 1, // Create Proxy pod
		},
		{
			name: "Multiple pod differences",
			desiredState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "new-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: nil,
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			currentState: func() *ClusterState {
				return &ClusterState{
					Nodes: []NodeState{
						{
							Charon: &IpmanPod[CharonPodSpec]{
								Meta: PodMeta{
									Name:      "charon-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "old-image",
								},
								Spec: CharonPodSpec{
									HostPath: "/test/path",
								},
							},
							Proxy: &IpmanPod[ProxyPodSpec]{
								Meta: PodMeta{
									Name:      "proxy-pod-test",
									Namespace: "ipman-system",
									Node:      "test-node",
									Image:     "test-image",
								},
								Spec: ProxyPodSpec{},
							},
							Xfrms: []IpmanPod[XfrmPodSpec]{},
						},
					},
				}
			},
			expectedActions: 3, // Delete and create Charon pod, delete Proxy pod
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fresh copies of states for each test run
			desiredState := tt.desiredState()
			currentState := tt.currentState()

			actions := DiffStates(desiredState, currentState)

			if actions == nil && tt.expectedActions > 0 {
				t.Fatalf("DiffStates() returned nil, expected %d actions", tt.expectedActions)
			}

			if actions != nil && len(actions) != tt.expectedActions {
				t.Errorf("DiffStates() returned %d actions, expected %d", len(actions), tt.expectedActions)
			}
		})
	}
}

// TestCharonPodImageChanged tests that changing a pod's image in desired state
// doesn't accidentally modify the current state due to shared pointers
func TestCharonPodImageChanged(t *testing.T) {
	// Create current state with a Charon pod directly in-line
	currentState := &ClusterState{
		Nodes: []NodeState{
			{
				Charon: &IpmanPod[CharonPodSpec]{
					Meta: PodMeta{
						Name:      "charon-pod-test",
						Namespace: "ipman-system",
						Node:      "test-node",
						Image:     "original-image",
					},
					Spec: CharonPodSpec{
						HostPath: "/test/path",
					},
				},
				Proxy: nil,
				Xfrms: []IpmanPod[XfrmPodSpec]{},
			},
		},
	}

	// Store the original image for verification later
	originalImage := currentState.Nodes[0].Charon.Meta.Image

	// Create a separate desired state with a different image
	desiredState := &ClusterState{
		Nodes: []NodeState{
			{
				Charon: &IpmanPod[CharonPodSpec]{
					Meta: PodMeta{
						Name:      "charon-pod-test",
						Namespace: "ipman-system",
						Node:      "test-node",
						Image:     "new-image", // Different image
					},
					Spec: CharonPodSpec{
						HostPath: "/test/path",
					},
				},
				Proxy: nil,
				Xfrms: []IpmanPod[XfrmPodSpec]{},
			},
		},
	}

	// Run DiffStates
	actions := DiffStates(desiredState, currentState)

	// Verify that changing the desired state didn't affect the current state
	if currentState.Nodes[0].Charon.Meta.Image != originalImage {
		t.Errorf("Current pod image was changed to %s, should still be '%s'",
			currentState.Nodes[0].Charon.Meta.Image, originalImage)
	}

	// Verify that actions were generated (should be 2: delete and create)
	if len(actions) != 2 {
		t.Errorf("Expected 2 actions (delete and create), got %d", len(actions))
	}
}
