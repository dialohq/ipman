package netconfig

import (
	"fmt"
	ip "github.com/vishvananda/netlink"
	"net"
	"strconv"
	"strings"
)

func FindDefaultInterface() (*ip.Link, error) {
	_, dflt, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		return nil, fmt.Errorf("Error parsing CIDR: %s", err.Error())
	}
	links, err := ip.LinkList()
	if err != nil {
		return nil, fmt.Errorf("Error listing links: %s", err.Error())
	}
	for _, l := range links {
		routes, err := ip.RouteList(l, ip.FAMILY_V4)
		if err != nil {
			return nil, fmt.Errorf("Error listing routes for device %s: %s", l.Attrs().Name, err.Error())
		}
		if len(routes) == 0 {
			continue
		}
		for _, r := range routes {
			if r.Dst.String() == dflt.String() {
				return &l, nil
			}
		}
	}
	return nil, fmt.Errorf("Default route not found")
}

func IpToByteArray(ip string) ([]byte, error) {
	octets := strings.Split(ip, ".")
	octet_bytes := []byte{}
	for _, o := range octets {
		i, err := strconv.ParseUint(o, 10, 8)
		if err != nil {
			return nil, err
		}
		octet_bytes = append(octet_bytes, byte(i))
	}
	return octet_bytes, nil
}
