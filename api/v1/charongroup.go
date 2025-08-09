package v1

import (
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

func (g *CharonGroup) Nsn() types.NamespacedName {
	return types.NamespacedName{Name: g.Name, Namespace: g.Namespace}
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
