package v1

import (
	"fmt"
	"sort"
)

// TODO: support other auth than PSK
type Child struct {
	Name        string          `json:"name"`
	LocalTs     string          `json:"localTs"`
	RemoteTs    string          `json:"remoteTs"`
	If_id       int             `json:"if_id"`
	Ip_pool     []string        `json:"ip_pool"`
	Xfrm_if_ip  string          `json:"xfrmIfIp"`
	Vxlan_if_ip string          `json:"vxlanIfIp"`
}

func (c *Child) SerializeToConf() string {
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
		`, c.Name, c.If_id, c.If_id, c.LocalTs, c.RemoteTs)
	return conf
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
	}
	return true
}
