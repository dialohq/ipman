package controller

import (
	"reflect"
	"testing"
)

func TestDiffRoutes(t *testing.T) {
	// Helper function to create a basic XfrmPod for testing
	createXfrmPod := func(name, node string, local, remote []string, bridgeFDB LocalRoutes) IpmanPod[XfrmPodSpec] {
		return IpmanPod[XfrmPodSpec]{
			Meta: PodMeta{
				Name:      name,
				Namespace: "ipman-system",
				NodeName:  node,
				Image:     "test-image",
			},
			Spec: XfrmPodSpec{
				Props: XfrmProperties{
					OwnerChild:      "test-child",
					OwnerConnection: "test-conn",
					InterfaceID:     101,
					XfrmIP:          "10.0.0.1/24",
					VxlanIP:         "10.0.0.2/24",
				},
				Routes: Routes{
					Local:     local,
					Remote:    remote,
					BridgeFDB: bridgeFDB,
				},
			},
		}
	}

	tests := []struct {
		name            string
		current         IpmanPod[XfrmPodSpec]
		desired         IpmanPod[XfrmPodSpec]
		expectedActions int
		validateActions func(t *testing.T, actions []Action)
	}{
		{
			name:            "Identical routes",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			expectedActions: 0,
			validateActions: nil,
		},
		{
			name:            "Add local route",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24", "10.0.3.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			expectedActions: 1,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 1 {
					t.Fatalf("Expected 1 action, got %d", len(actions))
				}

				addAction, ok := actions[0].(*AddLocalRouteAction)
				if !ok {
					t.Fatalf("Expected AddLocalRouteAction, got %T", actions[0])
				}

				if addAction.Route != "10.0.3.0/24" {
					t.Errorf("Expected route to be '10.0.3.0/24', got '%s'", addAction.Route)
				}
			},
		},
		{
			name:            "Remove local route",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24", "10.0.3.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			expectedActions: 1,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 1 {
					t.Fatalf("Expected 1 action, got %d", len(actions))
				}

				deleteAction, ok := actions[0].(*DeleteLocalRouteAction)
				if !ok {
					t.Fatalf("Expected DeleteLocalRouteAction, got %T", actions[0])
				}

				if deleteAction.Route != "10.0.3.0/24" {
					t.Errorf("Expected route to be '10.0.3.0/24', got '%s'", deleteAction.Route)
				}
			},
		},
		{
			name:            "Update local route",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24", "10.0.3.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24", "10.0.4.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			expectedActions: 2,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 2 {
					t.Fatalf("Expected 2 actions, got %d", len(actions))
				}

				// First action should be delete old route
				deleteAction, ok := actions[0].(*DeleteLocalRouteAction)
				if !ok {
					t.Fatalf("Expected first action to be DeleteLocalRouteAction, got %T", actions[0])
				}

				if deleteAction.Route != "10.0.3.0/24" {
					t.Errorf("Expected deleted route to be '10.0.3.0/24', got '%s'", deleteAction.Route)
				}

				// Second action should be add new route
				addAction, ok := actions[1].(*AddLocalRouteAction)
				if !ok {
					t.Fatalf("Expected second action to be AddLocalRouteAction, got %T", actions[1])
				}

				if addAction.Route != "10.0.4.0/24" {
					t.Errorf("Expected added route to be '10.0.4.0/24', got '%s'", addAction.Route)
				}
			},
		},
		{
			name:            "Add remote route",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24", "10.0.3.0/24"}, LocalRoutes{}),
			expectedActions: 1,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 1 {
					t.Fatalf("Expected 1 action, got %d", len(actions))
				}

				addAction, ok := actions[0].(*AddRemoteRouteAction)
				if !ok {
					t.Fatalf("Expected AddRemoteRouteAction, got %T", actions[0])
				}

				if addAction.Route != "10.0.3.0/24" {
					t.Errorf("Expected route to be '10.0.3.0/24', got '%s'", addAction.Route)
				}
			},
		},
		{
			name:            "Remove remote route",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24", "10.0.3.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			expectedActions: 1,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 1 {
					t.Fatalf("Expected 1 action, got %d", len(actions))
				}

				deleteAction, ok := actions[0].(*DeleteRemoteRouteAction)
				if !ok {
					t.Fatalf("Expected DeleteRemoteRouteAction, got %T", actions[0])
				}

				if deleteAction.Route != "10.0.3.0/24" {
					t.Errorf("Expected route to be '10.0.3.0/24', got '%s'", deleteAction.Route)
				}
			},
		},
		{
			name:            "Update remote route",
			current:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24", "10.0.3.0/24"}, LocalRoutes{}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24", "10.0.4.0/24"}, LocalRoutes{}),
			expectedActions: 2,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 2 {
					t.Fatalf("Expected 2 actions, got %d", len(actions))
				}

				// First action should be delete old route
				deleteAction, ok := actions[0].(*DeleteRemoteRouteAction)
				if !ok {
					t.Fatalf("Expected first action to be DeleteRemoteRouteAction, got %T", actions[0])
				}

				if deleteAction.Route != "10.0.3.0/24" {
					t.Errorf("Expected deleted route to be '10.0.3.0/24', got '%s'", deleteAction.Route)
				}

				// Second action should be add new route
				addAction, ok := actions[1].(*AddRemoteRouteAction)
				if !ok {
					t.Fatalf("Expected second action to be AddRemoteRouteAction, got %T", actions[1])
				}

				if addAction.Route != "10.0.4.0/24" {
					t.Errorf("Expected added route to be '10.0.4.0/24', got '%s'", addAction.Route)
				}
			},
		},
		{
			name:    "Add bridge FDB entry",
			current: createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			desired: createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"},
				LocalRoutes{"10.0.0.3": "192.168.1.1"}),
			expectedActions: 1,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 1 {
					t.Fatalf("Expected 1 action, got %d", len(actions))
				}

				addAction, ok := actions[0].(*AddBridgeFDBAction)
				if !ok {
					t.Fatalf("Expected AddBridgeFDBAction, got %T", actions[0])
				}

				if addAction.VxlanIP != "10.0.0.3" {
					t.Errorf("Expected VxlanIP to be '10.0.0.3', got '%s'", addAction.VxlanIP)
				}

				if addAction.UnderlyingIP != "192.168.1.1" {
					t.Errorf("Expected UnderlyingIP to be '192.168.1.1', got '%s'", addAction.UnderlyingIP)
				}
			},
		},
		{
			name: "Remove bridge FDB entry",
			current: createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"},
				LocalRoutes{"10.0.0.3": "192.168.1.1"}),
			desired:         createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"}, LocalRoutes{}),
			expectedActions: 1,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 1 {
					t.Fatalf("Expected 1 action, got %d", len(actions))
				}

				deleteAction, ok := actions[0].(*DeleteBridgeFDBAction)
				if !ok {
					t.Fatalf("Expected DeleteBridgeFDBAction, got %T", actions[0])
				}

				if deleteAction.VxlanIP != "10.0.0.3" {
					t.Errorf("Expected VxlanIP to be '10.0.0.3', got '%s'", deleteAction.VxlanIP)
				}

				if deleteAction.UnderlyingIP != "192.168.1.1" {
					t.Errorf("Expected UnderlyingIP to be '192.168.1.1', got '%s'", deleteAction.UnderlyingIP)
				}
			},
		},
		{
			name: "Update bridge FDB entry",
			current: createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"},
				LocalRoutes{"10.0.0.3": "192.168.1.1"}),
			desired: createXfrmPod("xfrm-pod-test", "node1", []string{"10.0.1.0/24"}, []string{"10.0.2.0/24"},
				LocalRoutes{"10.0.0.3": "192.168.1.2"}),
			expectedActions: 2,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 2 {
					t.Fatalf("Expected 2 actions, got %d", len(actions))
				}

				// First action should be delete old entry
				deleteAction, ok := actions[0].(*DeleteBridgeFDBAction)
				if !ok {
					t.Fatalf("Expected first action to be DeleteBridgeFDBAction, got %T", actions[0])
				}

				if deleteAction.VxlanIP != "10.0.0.3" {
					t.Errorf("Expected VxlanIP to be '10.0.0.3', got '%s'", deleteAction.VxlanIP)
				}

				if deleteAction.UnderlyingIP != "192.168.1.1" {
					t.Errorf("Expected UnderlyingIP to be '192.168.1.1', got '%s'", deleteAction.UnderlyingIP)
				}

				// Second action should be add new entry
				addAction, ok := actions[1].(*AddBridgeFDBAction)
				if !ok {
					t.Fatalf("Expected second action to be AddBridgeFDBAction, got %T", actions[1])
				}

				if addAction.VxlanIP != "10.0.0.3" {
					t.Errorf("Expected VxlanIP to be '10.0.0.3', got '%s'", addAction.VxlanIP)
				}

				if addAction.UnderlyingIP != "192.168.1.2" {
					t.Errorf("Expected UnderlyingIP to be '192.168.1.2', got '%s'", addAction.UnderlyingIP)
				}
			},
		},
		{
			name: "Multiple route changes",
			current: createXfrmPod("xfrm-pod-test", "node1",
				[]string{"10.0.1.0/24", "10.0.3.0/24"},
				[]string{"10.0.2.0/24", "10.0.4.0/24"},
				LocalRoutes{"10.0.0.3": "192.168.1.1"}),
			desired: createXfrmPod("xfrm-pod-test", "node1",
				[]string{"10.0.1.0/24", "10.0.5.0/24"},
				[]string{"10.0.2.0/24", "10.0.6.0/24"},
				LocalRoutes{"10.0.0.3": "192.168.1.2", "10.0.0.4": "192.168.1.3"}),
			expectedActions: 7,
			validateActions: func(t *testing.T, actions []Action) {
				if len(actions) != 7 {
					ts := []string{}
					for _, a := range actions {
						ts = append(ts, reflect.TypeOf(a).String())
					}
					t.Fatalf("Expected 7 actions, got %d, %+v", len(actions), ts)
				}

				// Count action types
				localDeleteCount := 0
				localAddCount := 0
				remoteDeleteCount := 0
				remoteAddCount := 0
				fdbDeleteCount := 0
				fdbAddCount := 0

				for _, action := range actions {
					switch action.(type) {
					case *DeleteLocalRouteAction:
						localDeleteCount++
					case *AddLocalRouteAction:
						localAddCount++
					case *DeleteRemoteRouteAction:
						remoteDeleteCount++
					case *AddRemoteRouteAction:
						remoteAddCount++
					case *DeleteBridgeFDBAction:
						fdbDeleteCount++
					case *AddBridgeFDBAction:
						fdbAddCount++
					default:
						t.Errorf("Unexpected action type: %T", action)
					}
				}

				if localDeleteCount != 1 || localAddCount != 1 ||
					remoteDeleteCount != 1 || remoteAddCount != 1 ||
					fdbDeleteCount != 1 || fdbAddCount != 2 {
					t.Errorf("Unexpected action counts: localDelete=%d, localAdd=%d, remoteDelete=%d, remoteAdd=%d, fdbDelete=%d, fdbAdd=%d",
						localDeleteCount, localAddCount, remoteDeleteCount, remoteAddCount, fdbDeleteCount, fdbAddCount)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions, err := diffRoutes(tt.current, tt.desired)
			if err != nil {
				t.Fatalf("diffRoutes() returned error: %v", err)
			}

			if len(actions) != tt.expectedActions {
				t.Errorf("diffRoutes() returned %d actions, expected %d", len(actions), tt.expectedActions)
			}

			if tt.validateActions != nil {
				tt.validateActions(t, actions)
			}
		})
	}
}
