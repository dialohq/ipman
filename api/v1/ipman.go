package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretRef holds a reference to a specific key in a Kubernetes Secret, including its name, namespace, and key.
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

// IPSecConnectionStatus represents the status of an IPSec connection, including gateway IPs, free IPs, pending IPs, and proxy IP.
type IPSecConnectionStatus struct {
	XfrmGatewayIPs map[string]string              `json:"xfrmGatewayIp"`
	FreeIPs        map[string]map[string][]string `json:"freeIps"`
	PendingIPs     map[string]string              `json:"pendingIps"`
	CharonProxyIP  string                         `json:"charonProxyIp"`
}

// IPSecConnection is the Kubernetes resource for configuring an IPSec connection.
type IPSecConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IPSecConnectionSpec   `json:"spec,omitempty"`
	Status IPSecConnectionStatus `json:"status,omitempty"`
}

// IPSecConnectionList contains a list of IPSecConnection resources.
type IPSecConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []IPSecConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IPSecConnection{}, &IPSecConnectionList{})
}
