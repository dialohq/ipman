package v1

import (
	"fmt"
	"reflect"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SecretRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

// TODO: support other auth than PSK
type Child struct {
	Name      string            `json:"name"`
	LocalTs   string            `json:"localTs"`
	RemoteTs  string            `json:"remoteTs"`
	PodNameIp map[string]string `json:"podNameIpMap"`
}

func (c *Child) SerializeToConf(if_id int) string {
	conf := fmt.Sprintf(`
			%s {
				if_id_in = %d
				if_id_out = %d
				local_ts = %s
				remote_ts = %s
			    esp_proposals = aes256-sha256-ecp256
			    start_action = start
			    dpd_action = restart
			    rekey_time = 1h
			}
		`, c.Name, if_id, if_id, c.LocalTs, c.RemoteTs)
	return conf
}

type IpmanSpec struct {
	Name       string    `json:"name"`
	RemoteAddr string    `json:"remoteAddr"`
	LocalId    string    `json:"localId"`
	RemoteId   string    `json:"remoteId"`
	SecretRef  SecretRef `json:"secretRef"`
	Children   []Child   `json:"children"`
	NodeName   string    `json:"nodeName"`
}

func (v *IpmanSpec) SerializeToConf(if_id int, secret string) string {
	serializedChildren := ""
	for _, child := range v.Children {
		serializedChildren += child.SerializeToConf(if_id)
	}
	conf := fmt.Sprintf(`
connections {
	%s {
		remote_addrs = %s
		local {
			auth = psk
			id = %s
		}
		remote {
			auth = psk
			id = %s
		}
		children {
			%s
		}
		version = 2
		proposals = aes256-sha256-ecp256
	}
}
secrets {
	ike-%s {
		secret = "%s"
		local-id = %s
		remote-id = %s
	}
}
		`, v.Name, v.RemoteAddr, v.LocalId, v.RemoteId, serializedChildren, v.Name, secret, v.LocalId, v.RemoteId)
	return conf
}

func (v IpmanSpec) DeepEqual(other IpmanSpec) bool {
	if v.Name != other.Name ||
		v.RemoteAddr != other.RemoteAddr ||
		v.LocalId != other.LocalId ||
		v.RemoteId != other.RemoteId ||
		v.NodeName != other.NodeName ||
		!secretRefEqual(v.SecretRef, other.SecretRef) ||
		!childrenEqual(v.Children, other.Children) {
		return false
	}
	return true
}

func secretRefEqual(a, b SecretRef) bool {
	return a.Name == b.Name &&
		a.Namespace == b.Namespace &&
		a.Key == b.Key
}

func childrenEqual(a, b []Child) bool {
	if len(a) != len(b) {
		return false
	}

	// order independent
	sort.Slice(a, func(i, j int) bool {
		return a[i].Name < a[j].Name
	})
	sort.Slice(b, func(i, j int) bool {
		return b[i].Name < b[j].Name
	})

	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
		if a[i].LocalTs != b[i].LocalTs {
			return false
		}
		if a[i].RemoteTs != b[i].RemoteTs {
			return false
		}
		if !reflect.DeepEqual(a[i].PodNameIp, b[i].PodNameIp) {
			return false
		}
	}
	return true
}

type IpmanStatus struct {
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
