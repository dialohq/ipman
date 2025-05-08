package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func secretRefEqual(a, b SecretRef) bool {
	return a.Name == b.Name &&
		a.Namespace == b.Namespace &&
		a.Key == b.Key
}


type IpmanStatus struct {
	XfrmGatewayIPs map[string]string              `json:"xfrmGatewayIp"`
	FreeIPs       map[string]map[string][]string `json:"freeIPs"`
	PendingIPs       map[string]string `json:"pendingIPs"`
}

type Ipman struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IpmanSpec   `json:"spec,omitempty"`
	Status IpmanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type IpmanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Ipman `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Ipman{}, &IpmanList{})
}
