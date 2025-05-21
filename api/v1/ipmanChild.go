package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

type Child struct {
	Name      string              `json:"name"`
	LocalIps  []string            `json:"local_ips"`
	RemoteIps []string            `json:"remote_ips"`
	XfrmIfId  int                 `json:"if_id"`
	IpPools   map[string][]string `json:"ip_pools"`
	XfrmIP    string              `json:"xfrm_ip"`
	VxlanIP   string              `json:"vxlan_ip"`
	Extra     map[string]string   `json:"extra,omitempty"`
}

func (c *Child) HashSum() string {
	out, _ := json.Marshal(c)
	hash := sha256.Sum256(out)
	return hex.EncodeToString(hash[:])
}

func (c *Child) EqualExceptChangable(c2 Child) bool {
	c_copy := c.DeepCopy()
	c2_copy := c2.DeepCopy()
	c_copy.IpPools = nil
	c2_copy.IpPools = nil
	c_copy.LocalIps = nil
	c2_copy.LocalIps = nil

	return reflect.DeepEqual(c_copy, c2_copy)
}

// TODO: migrate to go templates
func (c *Child) SerializeToConf() string {
	local_ts := strings.Join(c.LocalIps, ",")
	remote_ts := strings.Join(c.RemoteIps, ",")

	conf := fmt.Sprintf(`
			%s {
				if_id_in = %d
				if_id_out = %d
				local_ts = %s
				remote_ts = %s
				`, c.Name, c.XfrmIfId, c.XfrmIfId, local_ts, remote_ts)

	for k, v := range c.Extra {
		conf += fmt.Sprintf("%s = %s\n", k, v)
	}
	conf += "}\n"
	return conf
}

func childrenEqual(a, b map[string]Child) bool {
	keys_a := slices.Collect(maps.Keys(a))
	slices.Sort(keys_a)
	keys_b := slices.Collect(maps.Keys(b))
	slices.Sort(keys_b)

	if len(keys_a) != len(keys_b) {
		return false
	}

	if !reflect.DeepEqual(keys_a, keys_b) {
		return false
	}

	for k, v := range a {
		if !reflect.DeepEqual(v, b[k]) {
			return false
		}
	}
	return true
}
