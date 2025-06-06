package controller

import (
	"encoding/json"

	ipmanv1 "dialo.ai/ipman/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Route represents an IP address or CIDR used for routing
type Route string

// IpmanPodSpec is an interface for different pod specifications used in IPMan
type IpmanPodSpec interface {
	ApplySpec(*corev1.Pod, Envs)
	CompleteSetup(*IPSecConnectionReconciler, *corev1.Pod, string) error
	CompleteDeletion(*IPSecConnectionReconciler, *corev1.Pod, string) error
	CharonPodSpec | ProxyPodSpec | XfrmPodSpec
}

// IpmanPod is a generic container for IPMan pod resources with their specifications
type IpmanPod[Spec IpmanPodSpec] struct {
	Meta PodMeta `json:"meta" diff:"meta"`
	Spec Spec    `json:"spec" diff:"spec"`
}

// CreateK8sPodMeta creates a Kubernetes Pod object with metadata from the IpmanPod
func (p *IpmanPod[Spec]) CreateK8sPodMeta() corev1.Pod {
	var typeLabel string
	switch any(p.Spec).(type) {
	case CharonPodSpec:
		typeLabel = ipmanv1.LabelValueCharonPod
	case ProxyPodSpec:
		typeLabel = ipmanv1.LabelValueProxyPod
	case XfrmPodSpec:
		typeLabel = ipmanv1.LabelValueXfrmPod
	default:
		typeLabel = "Unknown"
	}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Meta.Name,
			Namespace: p.Meta.Namespace,
			Labels: map[string]string{
				ipmanv1.LabelPodType: typeLabel,
			},
			OwnerReferences: []metav1.OwnerReference{},
		},
	}
	an, _ := json.Marshal(p.Spec)
	pod.Spec = corev1.PodSpec{}
	pod.Annotations = map[string]string{
		ipmanv1.AnnotationSpec: string(an),
	}
	if p.Meta.Node != "" {
		pod.Spec.NodeSelector = map[string]string{
			ipmanv1.NodeSelectorHostName: p.Meta.Node,
		}
	}

	return pod
}

// CommonSpecs holds common spec data like
// image pull policies
type CommonSpecs struct {
	Image      string            `json:"image" diff:"image"`
	PullPolicy corev1.PullPolicy `json:"pull_policy" diff:"pull_policy"`
}

// Routes holds the local and remote routes for IPSec connections
type Routes struct {
	Local     []string    `json:"local_routes" diff:"local_routes"`
	Remote    []string    `json:"remote_routes" diff:"remote_routes"`
	BridgeFDB LocalRoutes `json:"bridge_fdb" diff:"bridge_fdb"`
}
type Owner struct {
	UID  string `json:"UID" diff:"UID"`
	Name string `json:"name" diff:"name"`
}

// PodMeta contains metadata for IPMan pods
type PodMeta struct {
	Name      string `json:"name" diff:"name"`
	Namespace string `json:"namespace" diff:"namespace"`
	IP        string `json:"ip" diff:"-"`
	Node      string `json:"node" diff:"node"`
	Image     string `json:"image" diff:"image"`
	Owner     Owner  `json:"owner" diff:"owner"`
}

// NodeState represents the state of all IPMan pods on a specific node
// +k8s:deepcopy-gen=true
type NodeState struct {
	Charon   *IpmanPod[CharonPodSpec] `json:"charon" diff:"charon"`
	Proxy    *IpmanPod[ProxyPodSpec]  `json:"proxy" diff:"proxy"`
	Xfrms    []IpmanPod[XfrmPodSpec]  `json:"xfrms" diff:"xfrms"`
	NodeName string                   `json:"name" diff:"name"`
}

// ClusterState represents the state of all nodes in the cluster
// +k8s:deepcopy-gen=true
type ClusterState struct {
	Nodes []NodeState `json:"nodes" diff:"nodes"`
}

type VxlanIP = string
type UnderlyingIP = string
type LocalRoutes map[VxlanIP]UnderlyingIP

type WorkerPodSpec struct {
	Routes          []Route `json:"routes" diff:"routes"`
	OwnerConnection string  `json:"owner_connection" diff:"owner_connection"`
	OwnerChild      string  `json:"owner_child" diff:"owner_child"`
	VxlanIP         string  `json:"vxlan_ip" diff:"vxlan_ip"`
}

type Worker struct {
	Meta PodMeta       `json:"metadata" diff:"metadata"`
	Spec WorkerPodSpec `json:"spec" diff:"spec"`
}

// ClusterState represents the state of all worker pods in the cluster
type WorkersState struct {
	Workers []Worker `json:"workers" diff:"workers"`
}
