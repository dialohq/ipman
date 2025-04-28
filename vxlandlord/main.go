package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	ip "github.com/vishvananda/netlink"
)

func findDefaultInterface() (*ip.Link, error) {
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

func createVxlan(underlying *ip.Link, local_ip net.IP, id int) (*ip.Link, error) {
	fmt.Println("Parent index: ", (*underlying).Attrs().Index)
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
		return nil, fmt.Errorf("Error adding vxlan interface: %s", err)
	}
	vxlan, err := ip.LinkByName(a.Name)
	if err != nil {
		return nil, fmt.Errorf("Error finding ip by alias: %s", err)
	}
	return &vxlan, nil
}

func ipToByteArray(ip string) []byte {
	octets := strings.Split(ip, ".")
	octet_bytes := []byte{}
	for _, o := range octets {

		i, err := strconv.ParseInt(o, 10, 8)
		if err != nil {
			fmt.Println("Malformed vxlan ip address")
		}
		octet_bytes = append(octet_bytes, byte(i))
	}
	return octet_bytes
}

type payload struct {
	MyIP string `json:"vxlan_pod_ip"`
}

// Every error is fatal since we want the
// pod to restart and try again
func fatal(err error, message string) {
	if err != nil {
		fmt.Println(fmt.Sprintf("%s: %s", message, err))
		os.Exit(1)
	}
}

// TODO: modularize this better
// this is duplicated in restctl-container
type vxlanInfo struct {
	If_id      int    `json:"vxlan_id,omitempty"`
	Vxlan_ip   string `json:"vxlan_ip,omitempty"`
	Gateway_ip string `json:"gateway_ip,omitempty"`
	Remote_ts  string `json:"remote_ts,omitempty"`
	Wait       int    `json:"wait"`
}

func main() {
	// find default interface ip
	iface, err := findDefaultInterface()
	fatal(err, "Error finding default interface")

	addrs, err := ip.AddrList(*iface, ip.FAMILY_V4)
	default_ip := addrs[0].IP
	fmt.Printf("IP identified as %s from interface %s", default_ip, (*iface).Attrs().Name)
	fatal(err, fmt.Sprintf("Error reading list of addresses of interface %s", (*iface).Attrs().Name))

	// ask charon-pod restctl api for data
	p, err := json.Marshal(&payload{MyIP: default_ip.String()})
	req, err := http.NewRequest("POST", "http://ipman-controller-service.ims.svc/vxlan", bytes.NewBuffer(p))
	fatal(err, "Error preparing reqeust to charon-pod")

	req.Header.Add("Content-Type", "application/json")
	client := &http.Client{}
	var vi vxlanInfo
	done := false
	for !done {
		res, err := client.Do(req)
		out, err := io.ReadAll(res.Body)
		fatal(err, "Error reading response body")

		err = json.Unmarshal(out, &vi)
		fatal(err, "Couldn't unmarshal response from charon-pod")
		if vi.Wait == 0 {
			done = true
		} else {
			time.Sleep(time.Duration(vi.Wait * int(time.Second)))
		}

	}

	ipa, subnet, _ := strings.Cut(vi.Vxlan_ip, "/")
	octets := strings.Split(ipa, ".")
	first_octets := strings.Join(octets[0:3], ".")
	xfrm_gateway_ip := first_octets + ".1"

	sn_int, err := strconv.ParseInt(subnet, 10, 6)
	fatal(err, "Error parsing subnet")

	ob := ipToByteArray(ipa)
	vxlan_ipnet := net.IPNet{
		IP:   net.IPv4(ob[0], ob[1], ob[2], ob[3]),
		Mask: net.CIDRMask(int(sn_int), 32),
	}
	fmt.Println("address: ", vxlan_ipnet)

	fmt.Println("vxlan_ipnet: ", vxlan_ipnet, vxlan_ipnet.IP)
	fatal(err, "Couldn't parse CIDR to net.IPNet")

	ob = ipToByteArray(xfrm_gateway_ip)
	xfrm_ipnet := net.IPNet{
		IP:   net.IPv4(ob[0], ob[1], ob[2], ob[3]),
		Mask: net.CIDRMask(int(sn_int), 32),
	}
	fatal(err, fmt.Sprintf("Error parsing CIDR xfrm_gateway_ip %s", xfrm_gateway_ip))

	vxlan, err := createVxlan(iface, addrs[0].IP, vi.If_id)
	fatal(err, "Error creating vxlan interface")

	err = ip.LinkSetUp(*vxlan)
	fatal(err, "Error settings vxlan interface up")

	ip.AddrAdd(*vxlan, &ip.Addr{IPNet: &vxlan_ipnet})
	cmd := exec.Command("bash", "-c", fmt.Sprintf("bridge fdb append 00:00:00:00:00:00 dev %s dst %s", "vxlan"+strconv.FormatInt(int64(vi.If_id), 10), vi.Gateway_ip))
	out, err := cmd.Output()
	if err != nil || string(out) != "" {
		fmt.Println("Error appending to bridge fdb: ", err)
		os.Exit(1)
	}

	_, remoteipnet, err := net.ParseCIDR(vi.Remote_ts)
	r := &ip.Route{
		LinkIndex: (*vxlan).Attrs().Index,
		Dst:       remoteipnet,
		Src:       vxlan_ipnet.IP,
		Gw:        xfrm_ipnet.IP,
	}
	err = ip.RouteAdd(r)
	fatal(err, "Error adding route")
}
