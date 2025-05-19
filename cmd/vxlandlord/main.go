package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"log/slog"

	"dialo.ai/ipman/pkg/netconfig"
	u "dialo.ai/ipman/pkg/utils"
	ip "github.com/vishvananda/netlink"
)

func createVxlan(underlying *ip.Link, local_ip net.IP, id int) (*ip.Link, error) {
	a := &ip.LinkAttrs{
		Name:    "vxlan" + strconv.FormatInt(int64(id), 10),
		NetNsID: -1,
		TxQLen:  -1,
	}

	vxlanTemplate := &ip.Vxlan{
		LinkAttrs:    *a,
		VxlanId:      id,
		Port:         4789,
		SrcAddr:      local_ip,
		VtepDevIndex: (*underlying).Attrs().Index,
	}

	err := ip.LinkAdd(vxlanTemplate)
	if err != nil {
		return nil, err
	}
	vxlan, err := ip.LinkByName(a.Name)
	if err != nil {
		return nil, err
	}
	return &vxlan, nil
}

func main() {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)

	ciliumInterface, err := netconfig.FindDefaultInterface()
	u.Fatal(err, logger, "Error finding default interface")

	addrs, err := ip.AddrList(*ciliumInterface, ip.FAMILY_V4)
	u.Fatal(err, logger, "Error listing addresses")

	if len(addrs) == 0 {
		u.Fatal(fmt.Errorf("List of addresses is empty"),
			logger,
			fmt.Sprintf("Error listing addresses for interface %s", (*ciliumInterface).Attrs().Name))
	}
	ciliumIP := addrs[0].IP
	logger.Info("Default IP identified", "IP", ciliumIP, "Interface", (*ciliumInterface).Attrs().Name)

	vxlanIp := os.Getenv("VXLAN_IP")
	xfrmIp := os.Getenv("XFRM_IP")
	ifId64, _ := strconv.ParseInt(os.Getenv("INTERFACE_ID"), 10, 64)
	ifId := int(ifId64)
	xfrmUnderlyingIp := os.Getenv("XFRM_GATEWAY_IP")
	remoteIpsCSV := os.Getenv("REMOTE_IPS")
	remoteIps := strings.Split(remoteIpsCSV, ",")

	vxlanIpAddr, subnetString, found := strings.Cut(vxlanIp, "/")
	if !found {
		u.Fatal(
			fmt.Errorf("Couldn't find separator '/'"),
			logger,
			fmt.Sprintf("Error cutting vxlan IP (%s)", vxlanIp),
		)
	}
	subnetInt, err := strconv.ParseInt(subnetString, 10, 8)
	u.Fatal(err, logger, fmt.Sprintf("Error parsing subnet (%s)", subnetString))

	IpBytes, err := netconfig.IpToByteArray(vxlanIpAddr)
	u.Fatal(err, logger, "Error converting IP of vxlan to byte array")
	vxlan_ipnet := net.IPNet{
		IP:   net.IPv4(IpBytes[0], IpBytes[1], IpBytes[2], IpBytes[3]),
		Mask: net.CIDRMask(int(subnetInt), 32),
	}

	xfrmIpAddr, _, _ := strings.Cut(xfrmIp, "/")
	IpBytes, err = netconfig.IpToByteArray(xfrmIpAddr)
	u.Fatal(err, logger, "Error converting IP of xfrm to byte array")
	xfrm_ipnet := net.IPNet{
		IP:   net.IPv4(IpBytes[0], IpBytes[1], IpBytes[2], IpBytes[3]),
		Mask: net.CIDRMask(int(subnetInt), 32),
	}
	vxlan, err := createVxlan(ciliumInterface, addrs[0].IP, ifId)
	u.Fatal(err, logger, "Error creating vxlan interface")

	err = ip.LinkSetUp(*vxlan)
	u.Fatal(err, logger, "Error settings vxlan interface up")

	ip.AddrAdd(*vxlan, &ip.Addr{IPNet: &vxlan_ipnet})
	if !u.IsValidIPv4(xfrmUnderlyingIp) {
		u.Fatal(
			fmt.Errorf("%s is not a valid ipv4 address", xfrmUnderlyingIp),
			logger,
			"Error preparing ip address to append to bridge fdb",
		)
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf("bridge fdb append 00:00:00:00:00:00 dev %s dst %s", "vxlan"+strconv.FormatInt(int64(ifId), 10), xfrmUnderlyingIp))
	_, err = cmd.Output()
	u.Fatal(err, logger, "Error appending to bridge fdb")

	dstWithFullMask := net.IPNet{IP: xfrm_ipnet.IP, Mask: net.CIDRMask(32, 32)}
	r := &ip.Route{
		LinkIndex: (*vxlan).Attrs().Index,
		Dst:       &dstWithFullMask,
		Src:       vxlan_ipnet.IP,
	}

	err = ip.RouteAdd(r)
	u.Fatal(err, logger, "Error adding route to xfrm interface", "route", r)

	logger.Info("Adding routes")
	for _, remote := range remoteIps {
		_, remoteIpNet, err := net.ParseCIDR(remote)
		r := &ip.Route{
			LinkIndex: (*vxlan).Attrs().Index,
			Dst:       remoteIpNet,
			Src:       vxlan_ipnet.IP,
			Gw:        xfrm_ipnet.IP,
			Flags:     int(ip.FLAG_ONLINK),
		}

		err = ip.RouteAdd(r)
		u.Fatal(err, logger, "Error adding route", "route", r)
		logger.Info("Added route", "route", r)
	}
}
