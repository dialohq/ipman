package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TODO: support other auth than PSK
type Child struct {
	Name      string              `json:"name"`
	LocalIps  []string            `json:"local_ips"`
	RemoteIps []string            `json:"remote_ips"`
	XfrmIfId  int                 `json:"if_id"`
	IpPools   map[string][]string `json:"ip_pools"`
	XfrmIP    string              `json:"xfrm_ip"`
	VxlanIP   string              `json:"vxlan_ip"`
}

func (c *Child)HashSum() string {
	out, _ := json.Marshal(c)
	hash := sha256.Sum256(out)
	return hex.EncodeToString(hash[:])
}

func (c *Child) SerializeToConf() string {
	local_ts := strings.Join(c.LocalIps, ",")
	remote_ts := strings.Join(c.RemoteIps, ",")

	conf := fmt.Sprintf(`
			%s {
				start_action = trap
				if_id_in = %d
				if_id_out = %d
				local_ts = %s
				remote_ts = %s
			    esp_proposals = aes256-sha256-ecp256
			    start_action = start
			    dpd_action = restart
			    rekey_time = 1h
			}
		`, c.Name, c.XfrmIfId, c.XfrmIfId, local_ts, remote_ts)
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
		for j := range a[i].LocalIps {
			if a[i].LocalIps[j] != b[i].LocalIps[j] {
				return false
			}
		}
		for j := range a[i].RemoteIps{
			if a[i].RemoteIps[j] != b[i].RemoteIps[j] {
				return false
			}
		}
	}
	return true
}
