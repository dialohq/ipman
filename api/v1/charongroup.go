package v1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=charon
// +kubebuilder:subresource:status
type CharonGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CharonGroupSpec   `json:"spec,omitempty"`
	Status CharonGroupStatus `json:"status,omitempty"`
}

func SameNsn[A, B HasNsn](a A, b B) bool {
	return a.Nsn() == b.Nsn()
}

func String[T HasNsn](t T) string {
	return t.Nsn().String()
}

type HasNsn interface {
	Nsn() types.NamespacedName
}

func (g *CharonGroup) Nsn() types.NamespacedName {
	return types.NamespacedName{Name: g.Name, Namespace: g.Namespace}
}

func (g *CharonGroup) String() string {
	return fmt.Sprintf("%s-%s", g.Name, g.Namespace)
}

func (g *CharonGroup) Equals(other HasNsn) bool {
	return SameNsn(g, other)
}

// +k8s:deepcopy-gen=true
type CharonGroupSpec struct {
	HostNetwork            bool              `json:"hostNetwork"`
	CharonExtraAnnotations map[string]string `json:"charonExtraAnnotations"`
	NodeName               string            `json:"nodeName"`
	InterfaceName          *string           `json:"interfaceName"`
}

type CharonGroupRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func (gr *CharonGroupRef) Nsn() types.NamespacedName {
	return types.NamespacedName{Name: gr.Name, Namespace: gr.Namespace}
}

func (gr *CharonGroupRef) Equals(other HasNsn) bool {
	return SameNsn(other, gr)
}

func (gr *CharonGroupRef) String() string {
	return String(gr)
}

type CharonGroupStatus struct {
}

// +kubebuilder:object:root=true
type CharonGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []CharonGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CharonGroup{}, &CharonGroupList{})
}
