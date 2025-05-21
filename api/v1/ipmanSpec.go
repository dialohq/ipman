package v1

import (
	"fmt"
	"strings"
)

type IpmanSpec struct {
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
	Secret string
	Ipman  Ipman
}

func (v *IpmanSpec) SerializeAllToConf(data []ConnData) string {
	conns := ""
	secrets := ""
	for _, d := range data {
		serializedChildren := ""
		for _, child := range d.Ipman.Spec.Children {
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
			d.Ipman.Spec.Name,
			d.Ipman.Spec.RemoteAddr,
			d.Ipman.Spec.LocalAddr,
			d.Ipman.Spec.LocalId,
			d.Ipman.Spec.RemoteId,
			serializedChildren,
		)
		for k, v := range d.Ipman.Spec.Extra {
			conns += fmt.Sprintf("%s = %s\n", k, v)
		}
		conns += "}\n"

		secrets += fmt.Sprintf(`
	%s {
		secret = "%s"
		local-id = %s
		remote-id = %s
	}
`,
			d.Ipman.Spec.SecretRef.Key,
			strings.Trim(d.Secret, " \n\t"),
			d.Ipman.Spec.LocalId,
			d.Ipman.Spec.RemoteId)
	}

	return fmt.Sprintf(`
connections {
	%s
}
secrets {
	%s
}`, conns, secrets)
}

func (v *IpmanSpec) SerializeToConf(secret string) string {
	serializedChildren := ""
	for _, child := range v.Children {
		serializedChildren += child.SerializeToConf()
	}
	conf := fmt.Sprintf(`
connections {
	%s {
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
}`,
		v.Name,
		v.RemoteAddr,
		v.LocalAddr,
		v.LocalId,
		v.RemoteId,
		serializedChildren,
		v.Name,
		secret,
		v.LocalId,
		v.RemoteId)
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
