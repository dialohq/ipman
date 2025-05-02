package v1

import (
	"fmt"
)

type IpmanSpec struct {
	Name       string    `json:"name"`
	RemoteAddr string    `json:"remoteAddr"`
	LocalAddr  string    `json:"localAddr"`
	LocalId    string    `json:"localId"`
	RemoteId   string    `json:"remoteId"`
	SecretRef  SecretRef `json:"secretRef"`
	Children   []Child   `json:"children"`
	NodeName   string    `json:"nodeName"`
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
