package controller

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	ipmanv1 "dialo.ai/ipman/api/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAddIPSecConnection(t *testing.T) {
	// Setup scheme for the fake client
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ipmanv1.AddToScheme(scheme)
	_ = promv1.AddToScheme(scheme)

	// Define the existing IPSec connection
	existingConnection := &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-connection",
			Namespace: "ipman-system",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       "existing",
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
			NodeName: "node1",
		},
	}

	// Define the nodes
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{
				MachineID: "aaabbbcccdddeeefff",
			},
		},
	}

	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node2",
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{
				MachineID: "fffeeedddcccbbbaaa",
			},
		},
	}

	// Create the initial cluster state objects
	// This simulates an already running IPSec connection with pods
	charonPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "charon-pod-aaabbbcccdddeeefff",
			Namespace: "ipman-system",
			Labels: map[string]string{
				ipmanv1.LabelPodType: ipmanv1.LabelValueCharonPod,
			},
			Annotations: map[string]string{
				ipmanv1.AnnotationSpec: `{"host_path":"/var/run/ipman"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				ipmanv1.NodeSelectorHostName: "node1",
			},
			NodeName: "node1",
			Containers: []corev1.Container{
				{
					Name:  "charon-daemon",
					Image: "test-charon-image",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      ipmanv1.CharonSocketHostVolumeName,
							MountPath: ipmanv1.CharonSocketVolumeMountPath,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				createCharonSocketVolume("/var/run/ipman"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.10.10.1",
		},
	}

	proxyPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "proxy-pod-aaabbbcccdddeeefff",
			Namespace: "ipman-system",
			Labels: map[string]string{
				ipmanv1.LabelPodType: ipmanv1.LabelValueProxyPod,
			},
			Annotations: map[string]string{
				ipmanv1.AnnotationSpec: `{"host_path":"/var/run/ipman"}`,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				ipmanv1.NodeSelectorHostName: "node1",
			},
			NodeName: "node1",
			Containers: []corev1.Container{
				{
					Name:  "proxy",
					Image: "test-caddy-image",
				},
			},
			Volumes: []corev1.Volume{
				createCharonSocketVolume("/var/run/ipman"),
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.10.10.2",
		},
	}

	xfrmPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xfrm-pod-child1-existing-connection",
			Namespace: "ipman-system",
			Labels: map[string]string{
				ipmanv1.LabelPodType: ipmanv1.LabelValueXfrmPod,
			},
			Annotations: map[string]string{
				ipmanv1.AnnotationSpec: `{
											 "routes":{
												"local_routes":["192.168.1.0/24"],
												"remote_routes":["192.168.2.0/24"],
												"bridge_fdb":{}
											  },
											  "properties":{
											  	"owner_child":"child1",
											  	"owner_connection":"existing-connection",
											  	"interface_id":101,
											  	"xfrm_ip":"192.168.1.1/24",
											  	"vxlan_ip":"192.168.1.2/24"
											   }
										   }`,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				ipmanv1.NodeSelectorHostName: "node1",
			},
			NodeName: "node1",
			Containers: []corev1.Container{
				{
					Name:  ipmanv1.XfrminionContainerName,
					Image: "test-xfrm-image",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.10.10.3",
		},
	}

	// Create a new IPSec connection to be added on a different node
	newConnection := &ipmanv1.IPSecConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-connection",
			Namespace: "ipman-system",
		},
		Spec: ipmanv1.IPSecConnectionSpec{
			Name:       "new",
			RemoteAddr: "10.0.0.3",
			LocalAddr:  "10.0.0.4",
			LocalId:    "10.0.0.4",
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
			NodeName: "node2", // Different node than the existing connection
		},
	}

	// Create a reconciler with the fake client containing the initial state
	reconciler := &IPSecConnectionReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingConnection, node1, node2, charonPod, proxyPod, xfrmPod).
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

	ctx := context.Background()

	// Get the current cluster state
	currentState, err := reconciler.GetClusterState(ctx)
	assert.NoError(t, err, "Getting current cluster state should not error")

	// Verify current state has one node with expected pods
	assert.Equal(t, 2, len(currentState.Nodes), "Current state should have 2 nodes")

	// Find the node1 state
	var node1State *NodeState
	for i := range currentState.Nodes {
		t.Logf("Node: %s, Xfrms: %d", currentState.Nodes[i].NodeName, len(currentState.Nodes[i].Xfrms))
		if len(currentState.Nodes[i].Xfrms) > 0 {
			for j, x := range currentState.Nodes[i].Xfrms {
				t.Logf("Xfrm pod %d: %+v", j, x)
				t.Logf("Xfrm pod %d Meta: %+v", j, x.Meta)
			}
		}
		if currentState.Nodes[i].NodeName == "node1" {
			node1State = &currentState.Nodes[i]
			break
		}
	}

	assert.NotNil(t, node1State, "Current state should include node1")
	assert.NotNil(t, node1State.Charon, "Node1 should have a Charon pod")
	assert.NotNil(t, node1State.Proxy, "Node1 should have a Proxy pod")
	assert.Equal(t, 1, len(node1State.Xfrms), "Node1 should have 1 Xfrm pod")
	assert.Equal(t, "xfrm-pod-child1-existing-connection", node1State.Xfrms[0].Meta.Name, "Xfrm pod name should match")

	// Add the new connection to the client
	err = reconciler.Create(ctx, newConnection)
	assert.NoError(t, err, "Creating new connection should not error")

	// Get the desired state (which should now include both connections)
	desiredState, err := reconciler.CreateDesiredState(ctx)
	assert.NoError(t, err, "Creating desired state should not error")

	// Verify desired state
	assert.Equal(t, 2, len(desiredState.Nodes), "Desired state should have 2 nodes")

	// Find the node states in the desired state
	var desiredNode1State, desiredNode2State *NodeState
	for i := range desiredState.Nodes {
		if desiredState.Nodes[i].NodeName == "node1" {
			desiredNode1State = &desiredState.Nodes[i]
		} else if desiredState.Nodes[i].NodeName == "node2" {
			desiredNode2State = &desiredState.Nodes[i]
		}
	}

	assert.NotNil(t, desiredNode1State, "Desired state should include node1")
	assert.NotNil(t, desiredNode2State, "Desired state should include node2")

	// Verify node1 state (should be unchanged)
	assert.NotNil(t, desiredNode1State.Charon, "Node1 should have a Charon pod")
	assert.NotNil(t, desiredNode1State.Proxy, "Node1 should have a Proxy pod")
	assert.Equal(t, 1, len(desiredNode1State.Xfrms), "Node1 should have 1 Xfrm pod")
	assert.Equal(t, "existing-connection", desiredNode1State.Xfrms[0].Spec.Props.OwnerConnection, "Node1 xfrm should be for existing connection")

	// Verify node2 state (should have new pods for the new connection)
	assert.NotNil(t, desiredNode2State.Charon, "Node2 should have a Charon pod")
	assert.NotNil(t, desiredNode2State.Proxy, "Node2 should have a Proxy pod")
	assert.Equal(t, 1, len(desiredNode2State.Xfrms), "Node2 should have 1 Xfrm pod")
	assert.Equal(t, "new-connection", desiredNode2State.Xfrms[0].Spec.Props.OwnerConnection, "Node2 xfrm should be for new connection")
	assert.Equal(t, "child2", desiredNode2State.Xfrms[0].Spec.Props.OwnerChild, "Node2 xfrm should be for child2")
	assert.Equal(t, []string{"192.168.3.0/24"}, desiredNode2State.Xfrms[0].Spec.Routes.Local, "Node2 xfrm should have correct local routes")
	assert.Equal(t, []string{"192.168.4.0/24"}, desiredNode2State.Xfrms[0].Spec.Routes.Remote, "Node2 xfrm should have correct remote routes")

	// Calculate the actions needed to reconcile
	actions, err := reconciler.DiffStates(desiredState, currentState, []ipmanv1.IPSecConnection{*existingConnection, *newConnection})
	assert.NoError(t, err, "DiffStates should not return an error")

	// Verify the actions
	// We expect several actions for setting up the new node:
	// 1. Create Charon pod on node2
	// 2. Create Proxy pod on node2
	// 3. Create Xfrm pod for the new connection on node2
	assert.NotEqual(t, 0, len(actions), "DiffStates should generate actions")

	// Count actions by type for node2 only
	node2CharonActions := 0
	node2ProxyActions := 0
	node2XfrmActions := 0
	node1CharonActions := 0
	node1ProxyActions := 0
	node1XfrmActions := 0

	for _, action := range actions {
		fmt.Println(reflect.TypeOf(action).String())
	}
	for _, action := range actions {
		switch a := action.(type) {
		case *CreatePodAction[CharonPodSpec]:
			if a.Pod.Meta.NodeName == "node2" {
				node2CharonActions++
			} else if a.Pod.Meta.NodeName == "node1" {
				node1CharonActions++
			}
		case *CreatePodAction[ProxyPodSpec]:
			if a.Pod.Meta.NodeName == "node2" {
				node2ProxyActions++
			} else if a.Pod.Meta.NodeName == "node1" {
				node1ProxyActions++
			}
		case *CreatePodAction[XfrmPodSpec]:
			if a.Pod.Meta.NodeName == "node2" {
				node2XfrmActions++
				assert.Equal(t, "new-connection", a.Pod.Spec.Props.OwnerConnection, "Xfrm pod should be for new connection")
				assert.Equal(t, "child2", a.Pod.Spec.Props.OwnerChild, "Xfrm pod should be for child2")
			} else if a.Pod.Meta.NodeName == "node1" {
				node1XfrmActions++
			}
		}
	}

	// We should have specific actions for node2 (the new node)
	assert.Equal(t, 1, node2CharonActions, "Should have 1 CreatePodAction for Charon pod on node2")
	assert.Equal(t, 1, node2ProxyActions, "Should have 1 CreatePodAction for Proxy pod on node2")
	assert.Equal(t, 1, node2XfrmActions, "Should have 1 CreatePodAction for Xfrm pod on node2")
	assert.Equal(t, 0, node1CharonActions, "Should have 0 CreatePodAction for Charon pod on node1")
	assert.Equal(t, 0, node1ProxyActions, "Should have 0 CreatePodAction for Proxy pod on node1")
	assert.Equal(t, 0, node1XfrmActions, "Should have 0 CreatePodAction for Xfrm pod on node1")
}
