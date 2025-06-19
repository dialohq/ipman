package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"reflect"
	"testing"

	ipmanv1 "dialo.ai/ipman/api/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/kr/pretty"
	"github.com/r3labs/diff/v3"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	c = ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sodies-nix",
			Namespace: "ipman-system",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       "3s",
			RemoteAddr: "13.51.6.188",
			LocalAddr:  "145.239.135.194",
			LocalId:    "145.239.135.194",
			RemoteId:   "13.51.6.188",
			SecretRef: ipmanv1.SecretRef{
				Name:      "ipsec-secret",
				Namespace: "default",
				Key:       "psk",
			},
			Children: map[string]ipmanv1.Child{
				"3s": {
					Name: "3s",
					Extra: map[string]string{
						"esp_proposals": "aes256-sha256-ecp256",
					},
					LocalIPs:  []string{"10.0.2.0/24"},
					RemoteIPs: []string{"10.0.1.0/24"},
					XfrmIP:    "10.0.2.1/24",
					VxlanIP:   "10.0.2.2/24",
					XfrmIfId:  101,
					IpPools: map[string][]string{
						"primary":   {"10.0.2.3/24", "10.0.2.4/24", "10.0.2.5/24", "10.0.2.6/24"},
						"secondary": {"10.0.2.7/24", "10.0.2.8/24", "10.0.2.9/24", "10.0.2.10/24"},
					},
				},
				"4s": {
					Name: "4s",
					Extra: map[string]string{
						"esp_proposals": "aes256-sha256-ecp256",
					},
					LocalIPs:  []string{"10.0.4.4/32", "10.0.9.8/32", "10.0.2.9/32", "10.0.4.3/32", "10.0.3.11/32", "10.0.23.20/32"},
					RemoteIPs: []string{"10.0.3.1/32", "10.0.3.3/32", "10.0.3.7/32"},
					XfrmIP:    "10.0.9.1/32",
					VxlanIP:   "10.0.8.2/32",
					XfrmIfId:  201,
					IpPools: map[string][]string{
						"worker":  {"10.0.9.8/32", "10.0.2.9/32"},
						"manager": {"10.0.4.3/32", "10.0.3.11/32", "10.0.23.20/32", "10.0.4.4/32"},
					},
				},
			},
			NodeName: "localcluster",
		},
	}
	c2 = ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sodies-nix2",
			Namespace: "ipman-system",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       "5s",
			RemoteAddr: "13.51.6.188",
			LocalAddr:  "145.239.135.194",
			LocalId:    "145.239.135.194",
			RemoteId:   "13.51.6.188",
			SecretRef: ipmanv1.SecretRef{
				Name:      "ipsec-secret",
				Namespace: "default",
				Key:       "psk",
			},
			Children: map[string]ipmanv1.Child{
				"7s": {
					Name: "7s",
					Extra: map[string]string{
						"esp_proposals": "aes256-sha256-ecp256",
					},
					LocalIPs:  []string{"10.0.2.0/24"},
					RemoteIPs: []string{"10.0.1.0/24"},
					XfrmIP:    "10.0.2.1/24",
					VxlanIP:   "10.0.2.2/24",
					XfrmIfId:  102,
					IpPools: map[string][]string{
						"primary":   {"10.0.2.3/24", "10.0.2.4/24", "10.0.2.5/24", "10.0.2.6/24"},
						"secondary": {"10.0.2.7/24", "10.0.2.8/24", "10.0.2.9/24", "10.0.2.10/24"},
					},
				},
				"8s": {
					Name: "8s",
					Extra: map[string]string{
						"esp_proposals": "aes256-sha256-ecp256",
					},
					LocalIPs:  []string{"10.0.4.4/32", "10.0.9.8/32", "10.0.2.9/32", "10.0.4.3/32", "10.0.3.11/32", "10.0.23.20/32"},
					RemoteIPs: []string{"10.0.3.1/32", "10.0.3.3/32", "10.0.3.7/32"},
					XfrmIP:    "10.0.9.1/32",
					VxlanIP:   "10.0.8.2/32",
					XfrmIfId:  202,
					IpPools: map[string][]string{
						"worker":  {"10.0.9.8/32", "10.0.2.9/32"},
						"manager": {"10.0.4.3/32", "10.0.3.11/32", "10.0.23.20/32", "10.0.4.4/32"},
					},
				},
			},
			NodeName: "localcluster2",
		},
	}
	scheme = runtime.NewScheme()
	_      = clientgoscheme.AddToScheme(scheme) // register core types
	_      = ipmanv1.AddToScheme(scheme)
	_      = promv1.AddToScheme(scheme)
	r      = &IPSecConnectionReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects().Build(),
		Scheme: scheme,
		Env: Envs{
			NamespaceName:       "ipman-system",
			HostSocketsPath:     "/var/run/ipman",
			XfrminionImage:      "plan9better/xfrminion:latest-dev",
			CharonDaemonImage:   "plan9better/strongswan-charon:0.0.7",
			VxlandlordImage:     "plan9better/vxlandlord:latest-dev",
			RestctlImage:        "plan9better/restctl:latest-dev",
			CaddyImage:          "caddy:2.10.0-alpine",
			XfrminionPullPolicy: "Always",
		},
	}
)

func TestCreatingDesiredState(t *testing.T) {
	ctx := context.Background()

	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&c,
		&c2,
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "localcluster",
			},
			Status: corev1.NodeStatus{
				NodeInfo: corev1.NodeSystemInfo{
					MachineID: "aaabbbcccdddeeefff",
				},
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "localcluster2",
			},
			Status: corev1.NodeStatus{
				NodeInfo: corev1.NodeSystemInfo{
					MachineID: "fffeeedddcccbbbaaa",
				},
			},
		},
	).Build()
	actualDs := &ClusterState{
		Nodes: []NodeState{
			{
				Charon: &IpmanPod[CharonPodSpec]{
					Meta: PodMeta{
						Name:      "charon-pod-aaabbbcccdddeeefff",
						Namespace: "ipman-system",
						NodeName:  "localcluster",
						NodeID:    "aaabbbcccdddeeefff",
						Image:     "plan9better/strongswan-charon:0.0.7",
					},
					Spec: CharonPodSpec{
						HostPath: "/var/run/ipman",
					},
				},
				Proxy: &IpmanPod[ProxyPodSpec]{
					Meta: PodMeta{
						Name:      "proxy-pod-aaabbbcccdddeeefff",
						Namespace: "ipman-system",
						NodeName:  "localcluster",
						NodeID:    "aaabbbcccdddeeefff",
						Image:     "caddy:2.10.0-alpine",
					},
					Spec: ProxyPodSpec{
						HostPath: "/var/run/ipman",
						Configs:  []ipmanv1.IPSecConnectionSpec{c.Spec},
					},
				},
				Xfrms: []IpmanPod[XfrmPodSpec]{
					{
						Meta: PodMeta{
							NodeID:    "aaabbbcccdddeeefff",
							Name:      "xfrm-pod-3s-sodies-nix",
							Namespace: "ipman-system",
							NodeName:  "localcluster",
							Image:     "plan9better/xfrminion:latest-dev",
						},
						Spec: XfrmPodSpec{
							Routes: Routes{
								Local:     []string{"10.0.2.0/24"},
								Remote:    []string{"10.0.1.0/24"},
								BridgeFDB: LocalRoutes{},
							},
							Props: XfrmProperties{
								OwnerChild:      "3s",
								OwnerConnection: "sodies-nix",
								InterfaceID:     101,
								XfrmIP:          "10.0.2.1/24",
								VxlanIP:         "10.0.2.2/24",
							},
						},
					},
					{
						Meta: PodMeta{
							Name:      "xfrm-pod-4s-sodies-nix",
							Namespace: "ipman-system",
							NodeName:  "localcluster",
							NodeID:    "aaabbbcccdddeeefff",
							Image:     "plan9better/xfrminion:latest-dev",
						},
						Spec: XfrmPodSpec{
							Routes: Routes{
								Local:     []string{"10.0.4.4/32", "10.0.9.8/32", "10.0.2.9/32", "10.0.4.3/32", "10.0.3.11/32", "10.0.23.20/32"},
								Remote:    []string{"10.0.3.1/32", "10.0.3.3/32", "10.0.3.7/32"},
								BridgeFDB: LocalRoutes{},
							},
							Props: XfrmProperties{
								OwnerChild:      "4s",
								OwnerConnection: "sodies-nix",
								XfrmIP:          "10.0.9.1/32",
								VxlanIP:         "10.0.8.2/32",
								InterfaceID:     201,
							},
						},
					},
				},
				NodeName:  "localcluster",
				MachineID: "aaabbbcccdddeeefff",
			}, {
				Charon: &IpmanPod[CharonPodSpec]{
					Meta: PodMeta{
						Name:      "charon-pod-fffeeedddcccbbbaaa",
						Namespace: "ipman-system",
						NodeID:    "fffeeedddcccbbbaaa",
						NodeName:  "localcluster2",
						Image:     "plan9better/strongswan-charon:0.0.7",
					},
					Spec: CharonPodSpec{
						HostPath: "/var/run/ipman",
					},
				},
				Proxy: &IpmanPod[ProxyPodSpec]{
					Meta: PodMeta{
						Name:      "proxy-pod-fffeeedddcccbbbaaa",
						Namespace: "ipman-system",
						NodeID:    "fffeeedddcccbbbaaa",
						NodeName:  "localcluster2",
						Image:     "caddy:2.10.0-alpine",
					},
					Spec: ProxyPodSpec{
						HostPath: "/var/run/ipman",
						Configs:  []ipmanv1.IPSecConnectionSpec{c2.Spec},
					},
				},
				Xfrms: []IpmanPod[XfrmPodSpec]{
					{
						Meta: PodMeta{
							Name:      "xfrm-pod-7s-sodies-nix2",
							Namespace: "ipman-system",
							NodeName:  "localcluster2",
							NodeID:    "fffeeedddcccbbbaaa",
							Image:     "plan9better/xfrminion:latest-dev",
						},
						Spec: XfrmPodSpec{
							Routes: Routes{
								Local:     []string{"10.0.2.0/24"},
								Remote:    []string{"10.0.1.0/24"},
								BridgeFDB: LocalRoutes{},
							},
							Props: XfrmProperties{
								OwnerChild:      "7s",
								OwnerConnection: "sodies-nix2",
								InterfaceID:     102,
								XfrmIP:          "10.0.2.1/24",
								VxlanIP:         "10.0.2.2/24",
							},
						},
					},
					{
						Meta: PodMeta{
							Name:      "xfrm-pod-8s-sodies-nix2",
							Namespace: "ipman-system",
							NodeID:    "fffeeedddcccbbbaaa",
							NodeName:  "localcluster2",
							Image:     "plan9better/xfrminion:latest-dev",
						},
						Spec: XfrmPodSpec{
							Routes: Routes{
								Local:     []string{"10.0.4.4/32", "10.0.9.8/32", "10.0.2.9/32", "10.0.4.3/32", "10.0.3.11/32", "10.0.23.20/32"},
								Remote:    []string{"10.0.3.1/32", "10.0.3.3/32", "10.0.3.7/32"},
								BridgeFDB: LocalRoutes{},
							},
							Props: XfrmProperties{
								OwnerChild:      "8s",
								OwnerConnection: "sodies-nix2",
								XfrmIP:          "10.0.9.1/32",
								VxlanIP:         "10.0.8.2/32",
								InterfaceID:     202,
							},
						},
					},
				},
				NodeName:  "localcluster2",
				MachineID: "fffeeedddcccbbbaaa",
			},
		},
	}
	ds, err := r.CreateDesiredState(ctx)
	if err != nil {
		t.Errorf("Creating desired state returned an error: %v, desired: %v, received: %v", err, actualDs, ds)
	}

	slices.SortFunc(ds.Nodes, func(a, b NodeState) int {
		return strings.Compare(a.MachineID, b.MachineID)
	})
	slices.SortFunc(actualDs.Nodes, func(a, b NodeState) int {
		return strings.Compare(a.MachineID, b.MachineID)
	})

	if !reflect.DeepEqual(ds, actualDs) {
		d, _ := diff.Diff(actualDs, ds)
		for _, df := range d {
			fmt.Printf("%+v\n", df)
		}
		t.Errorf("States are not equal")
	}
}

func TestReadingEmptyClusterState(t *testing.T) {
	ctx := context.Background()
	objs := []client.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "localcluster",
			},
		},
	}
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	s, err := r.GetClusterState(ctx)
	if err != nil {
		t.Errorf("Got error getting cluster state: %s", err)
	}
	actualState := &ClusterState{
		Nodes: []NodeState{
			{
				Charon:   nil,
				Proxy:    nil,
				Xfrms:    []IpmanPod[XfrmPodSpec]{},
				NodeName: "localcluster",
			},
		},
	}
	if !reflect.DeepEqual(s, actualState) {
		ds := pretty.Diff(actualState, s)
		for _, d := range ds {
			fmt.Println(d)
		}
		fmt.Println(s)
		fmt.Println(actualState)
		t.Errorf("States don't match")
	}
}

func TestDiffStates(t *testing.T) {
	// Test case 1: States are identical, no actions should be returned
	desiredState := &ClusterState{
		Nodes: []NodeState{
			{
				Charon:   nil,
				Proxy:    nil,
				Xfrms:    []IpmanPod[XfrmPodSpec]{},
				NodeName: "localcluster",
			},
		},
	}
	currentState := &ClusterState{
		Nodes: []NodeState{
			{
				Charon:   nil,
				Proxy:    nil,
				Xfrms:    []IpmanPod[XfrmPodSpec]{},
				NodeName: "localcluster",
			},
		},
	}

	testReconciler := createTestReconciler()
	actions, err := testReconciler.DiffStates(desiredState, currentState, []ipmanv1.IPSecConnection{})
	if err != nil {
		t.Fatalf("DiffStates() returned an error: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("Expected no actions for identical states, got %d actions", len(actions))
	}

	// Test case 2: Missing Charon pod in current state, should create it
	desiredWithCharon := &ClusterState{
		Nodes: []NodeState{
			{
				Charon: &IpmanPod[CharonPodSpec]{
					Meta: PodMeta{
						Name:      "charon-pod-test",
						Namespace: "ipman-system",
						NodeName:  "localcluster",
						Image:     "test-image",
					},
					Spec: CharonPodSpec{
						HostPath: "/test/path",
					},
				},
				Proxy:    nil,
				Xfrms:    []IpmanPod[XfrmPodSpec]{},
				NodeName: "localcluster",
			},
		},
	}

	actions, err = testReconciler.DiffStates(desiredWithCharon, currentState, []ipmanv1.IPSecConnection{})
	if err != nil {
		t.Fatalf("DiffStates() returned an error: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("Expected 1 action for missing Charon pod, got %d actions", len(actions))
	}

	// Verify the action is a CreatePodAction for Charon
	createAction, ok := actions[0].(*CreatePodAction[CharonPodSpec])
	if !ok {
		t.Errorf("Expected *CreatePodAction[CharonPodSpec], got %T", actions[0])
	}

	if createAction.Pod.Meta.Name != "charon-pod-test" {
		t.Errorf("Expected action for pod 'charon-pod-test', got '%s'", createAction.Pod.Meta.Name)
	}

	// Test case 3: When a pod exists in current state but not in desired state, it should be deleted
	// This test is designed for how DiffStates should work in the future
	currentWithCharon := &ClusterState{
		Nodes: []NodeState{
			{
				Charon: &IpmanPod[CharonPodSpec]{
					Meta: PodMeta{
						Name:      "charon-pod-to-delete",
						Namespace: "ipman-system",
						NodeName:  "localcluster",
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

	emptyDesired := &ClusterState{
		Nodes: []NodeState{
			{
				Charon: nil,
				Proxy:  nil,
				Xfrms:  []IpmanPod[XfrmPodSpec]{},
			},
		},
	}

	// This test is expected to fail with the current implementation, but shows what we expect
	// in the future once DeletePodAction is properly implemented
	t.Run("Delete pod when not in desired state", func(t *testing.T) {
		// Skip this test for now as it's for future functionality
		// t.Skip("DeletePodAction not fully implemented yet")

		actions, err = testReconciler.DiffStates(emptyDesired, currentWithCharon, []ipmanv1.IPSecConnection{})
		if err != nil {
			t.Fatalf("DiffStates() returned an error: %v", err)
		}
		if len(actions) != 1 {
			t.Errorf("Expected 1 delete action, got %d actions", len(actions))
		}

		deleteAction, ok := actions[0].(*DeletePodAction[CharonPodSpec])
		if !ok {
			t.Errorf("Expected DeletePodAction[CharonPodSpec], got %T", actions[0])
		}

		if deleteAction.Pod.Meta.Name != "charon-pod-to-delete" {
			t.Errorf("Expected action for pod 'charon-pod-to-delete', got '%s'", deleteAction.Pod.Meta.Name)
		}
	})
}

func TestMultipleIPSecConnections(t *testing.T) {
	ctx := context.Background()

	// Create two IPSecConnections on the same node
	conn1 := ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "connection-1",
			Namespace: "ipman-system",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       "conn1",
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
			NodeName: "localcluster",
		},
	}

	conn2 := ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "connection-2",
			Namespace: "ipman-system",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       "conn2",
			RemoteAddr: "10.0.0.3",
			LocalAddr:  "10.0.0.2",
			LocalId:    "10.0.0.2",
			RemoteId:   "10.0.0.3",
			SecretRef: ipmanv1.SecretRef{
				Name:      "secret-2",
				Namespace: "default",
				Key:       "psk",
			},
			Children: map[string]ipmanv1.Child{
				"child2": {
					Name: "child2",
					Extra: map[string]string{
						"esp_proposals": "aes256-sha256-ecp256",
					},
					LocalIPs:  []string{"192.168.3.0/24"},
					RemoteIPs: []string{"192.168.4.0/24"},
					XfrmIP:    "192.168.3.1/24",
					VxlanIP:   "192.168.3.2/24",
					XfrmIfId:  102,
					IpPools: map[string][]string{
						"pool2": {"192.168.3.3/24", "192.168.3.4/24"},
					},
				},
			},
			NodeName: "localcluster",
		},
	}

	// Create a test reconciler with both connections and a node
	testReconciler := &IPSecConnectionReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&conn1, &conn2, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "localcluster"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{MachineID: "aaabbbcccdddeeefff"}}}).
			Build(),
		Scheme: scheme,
		Env: Envs{
			NamespaceName:       "ipman-system",
			HostSocketsPath:     "/var/run/ipman",
			XfrminionImage:      "test-xfrm-image",
			CharonDaemonImage:   "test-charon-image",
			VxlandlordImage:     "test-vxlan-image",
			RestctlImage:        "test-restctl-image",
			CaddyImage:          "test-caddy-image",
			XfrminionPullPolicy: "Always",
		},
	}

	// Get the desired state
	desiredState, err := testReconciler.CreateDesiredState(ctx)
	if err != nil {
		t.Fatalf("Error creating desired state: %v", err)
	}

	// Verify there's one node with both connections' resources
	if len(desiredState.Nodes) != 1 {
		t.Errorf("Expected 1 node, got %d", len(desiredState.Nodes))
	}

	node := desiredState.Nodes[0]

	// Should have 1 Charon pod
	if node.Charon == nil {
		t.Errorf("Expected Charon pod, got nil")
	} else {
		expectedName := "charon-pod-aaabbbcccdddeeefff"
		if node.Charon.Meta.Name != expectedName {
			t.Errorf("Expected Charon pod name %s, got %s", expectedName, node.Charon.Meta.Name)
		}
	}

	// Should have 1 Proxy pod
	if node.Proxy == nil {
		t.Errorf("Expected Proxy pod, got nil")
	} else {
		expectedName := "proxy-pod-aaabbbcccdddeeefff"
		if node.Proxy.Meta.Name != expectedName {
			t.Errorf("Expected Proxy pod name %s, got %s", expectedName, node.Proxy.Meta.Name)
		}
	}

	// Should have 2 Xfrm pods (one for each child)
	if len(node.Xfrms) != 2 {
		t.Errorf("Expected 2 Xfrm pods, got %d", len(node.Xfrms))
	}

	// Verify that the Xfrm pods have the correct properties
	foundChild1 := false
	foundChild2 := false

	for _, xfrm := range node.Xfrms {
		if xfrm.Spec.Props.OwnerChild == "child1" && xfrm.Spec.Props.OwnerConnection == "connection-1" {
			foundChild1 = true
			if xfrm.Spec.Props.XfrmIP != "192.168.1.1/24" {
				t.Errorf("Expected XfrmIP 192.168.1.1/24, got %s", xfrm.Spec.Props.XfrmIP)
			}
			if xfrm.Spec.Props.VxlanIP != "192.168.1.2/24" {
				t.Errorf("Expected VxlanIP 192.168.1.2/24, got %s", xfrm.Spec.Props.VxlanIP)
			}
			if xfrm.Spec.Props.InterfaceID != 101 {
				t.Errorf("Expected InterfaceID 101, got %d", xfrm.Spec.Props.InterfaceID)
			}
		}

		if xfrm.Spec.Props.OwnerChild == "child2" && xfrm.Spec.Props.OwnerConnection == "connection-2" {
			foundChild2 = true
			if xfrm.Spec.Props.XfrmIP != "192.168.3.1/24" {
				t.Errorf("Expected XfrmIP 192.168.3.1/24, got %s", xfrm.Spec.Props.XfrmIP)
			}
			if xfrm.Spec.Props.VxlanIP != "192.168.3.2/24" {
				t.Errorf("Expected VxlanIP 192.168.3.2/24, got %s", xfrm.Spec.Props.VxlanIP)
			}
			if xfrm.Spec.Props.InterfaceID != 102 {
				t.Errorf("Expected InterfaceID 102, got %d", xfrm.Spec.Props.InterfaceID)
			}
		}
	}

	if !foundChild1 {
		t.Errorf("Did not find Xfrm pod for child1")
	}

	if !foundChild2 {
		t.Errorf("Did not find Xfrm pod for child2")
	}
}

func TestDeletePodAction(t *testing.T) {

	ctx := context.Background()

	// Create a mock client and test pod
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
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

	// Create a fake client with the test pod
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testPod).
		Build()

	// Create a reconciler with the fake client
	reconciler := &IPSecConnectionReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Create a DeletePodAction with a pod that references the test pod
	podToDelete := &IpmanPod[CharonPodSpec]{
		Meta: PodMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
		},
		Spec: CharonPodSpec{
			HostPath: "/test/path",
		},
	}

	deleteAction := DeletePodAction[CharonPodSpec]{
		Pod: podToDelete,
	}

	// In the future implementation, this would execute the DeletePodAction.Do method
	// and verify the pod was deleted from the client
	err := deleteAction.Do(ctx, reconciler)
	if err != nil {
		t.Errorf("Error executing DeletePodAction: %v", err)
	}

	// Verify the pod was deleted
	err = reconciler.Get(ctx, client.ObjectKey{Name: "test-pod", Namespace: "test-namespace"}, &corev1.Pod{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("Expected pod to be deleted, but it still exists")
	}

	// For now, just ensure the method doesn't panic
	t.Run("DeletePodAction.Do method should not panic", func(t *testing.T) {
		// We use the old implementation which just prints to stdout
		deleteAction.Do(ctx, reconciler)
		// No assertion, we just verify it doesn't panic
	})
}
