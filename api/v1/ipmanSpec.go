package v1

import (
	"fmt"
	"strings"
)

type IPSecConnectionSpec struct {
	Name       string            `json:"name"`
	RemoteAddr string            `json:"remoteAddr"`
	LocalAddr  string            `json:"localAddr"`
	LocalId    string            `json:"localId"`
	RemoteId   string            `json:"remoteId"`
	SecretRef  SecretRef         `json:"secretRef"`
	Children   map[string]Child  `json:"children"`
	NodeName   string            `json:"nodeName"`
	Extra      map[string]string `json:"extra,omitempty"`
}

type ConnData struct {
	Secret          string
	IPSecConnection IPSecConnection
}

func (v *IPSecConnectionSpec) SerializeAllToConf(data []ConnData) string {
	conns := ""
	secrets := ""
	for _, d := range data {
		serializedChildren := ""
		for _, child := range d.IPSecConnection.Spec.Children {
			serializedChildren += child.SerializeToConf()
		}
		conns += fmt.Sprintf(`%s {
		remote_addrs = %s
		local_addrs = %s
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
`,
			d.IPSecConnection.Spec.Name,
			d.IPSecConnection.Spec.RemoteAddr,
			d.IPSecConnection.Spec.LocalAddr,
			d.IPSecConnection.Spec.LocalId,
			d.IPSecConnection.Spec.RemoteId,
			serializedChildren,
		)
		for k, v := range d.IPSecConnection.Spec.Extra {
			conns += fmt.Sprintf("%s = %s\n", k, v)
		}
		conns += "}\n"

		secrets += fmt.Sprintf(`
	ike-%s {
		secret = "%s"
		local-id = %s
		remote-id = %s
	}
`,
			d.IPSecConnection.Spec.SecretRef.Key,
			strings.Trim(d.Secret, " \n\t"),
			d.IPSecConnection.Spec.LocalId,
			d.IPSecConnection.Spec.RemoteId)
	}

	return fmt.Sprintf(`
connections {
	%s
}
secrets {
	%s
}`, conns, secrets)
}

func (v IPSecConnectionSpec) DeepEqual(other IPSecConnectionSpec) bool {
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
