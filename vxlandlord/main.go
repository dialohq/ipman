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
	fmt.Println("Finding default interface")
	_, dflt, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		return nil, fmt.Errorf("Error parsing CIDR: %s", err.Error())
	}
	links, err := ip.LinkList()
	fmt.Println("listing links")
	if err != nil {
		return nil, fmt.Errorf("Error listing links: %s", err.Error())
	}
	fmt.Println("looping")
	for _, l := range links {
		fmt.Println("link: ", l.Attrs().Name)
		routes, err := ip.RouteList(l, ip.FAMILY_V4)
		if err != nil {
			return nil, fmt.Errorf("Error listing routes for device %s: %s", l.Attrs().Name, err.Error())
		}
		if len(routes) == 0 {
			continue
		}
		fmt.Println("looping through routes")
		for _, r := range routes {
			if r.Dst.String() == dflt.String() {
				fmt.Println("returinng")
				return &l, nil
			}
		}
	}
	fmt.Println("returinng")
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

		i, err := strconv.ParseUint(o, 10, 8)
		if err != nil {
			fmt.Println("o", o)
			fmt.Println("Malformed vxlan ip address", err)
			return nil
		}
		octet_bytes = append(octet_bytes, byte(i))
	}
	return octet_bytes
}

type payload struct {
	MyIP      string `json:"vxlan_pod_ip"`
	ChildName string `json:"child_name"`
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
	If_id     int    `json:"vxlan_id,omitempty"`
	Remote_ts string `json:"remote_ts,omitempty"`
	Xfrm_if_ip string `json:"xfrm_if_ip"`
	Xfrm_underlying_ip string `json:"xfrm_underlying_ip"`
	Wait      int    `json:"wait"`
}

func main() {
	// find default interface ip
	iface, err := findDefaultInterface()
	fatal(err, "Error finding default interface")

	fmt.Println("addrs")
	addrs, err := ip.AddrList(*iface, ip.FAMILY_V4)
	fatal(err, "Error listing addresses")
	fmt.Println("addrs: ", addrs)
	default_ip := addrs[0].IP
	fmt.Println("Default ip: ", default_ip)
	fmt.Println("iface: ", iface, (*iface), (*iface).Attrs())
	fmt.Println("IP identified as", default_ip, "from interface ", (*iface).Attrs().Name)
	fatal(err, fmt.Sprintf("Error reading list of addresses of interface %s", (*iface).Attrs().Name))

	// ask charon-pod restctl api for data
	vxlanip := os.Getenv("VXLAN_IP")
	childName := os.Getenv("CHILD_NAME")
	xfrm_gateway := os.Getenv("XFRM_GATEWAY")
	fmt.Println("env vars: ", xfrm_gateway, vxlanip, childName)
	p, err := json.Marshal(&payload{MyIP: default_ip.String(), ChildName: childName})
	fatal(err, "Error marshaling reqeust to charon-pod")
	fmt.Println("marshaled: ", string(p))
	req, err := http.NewRequest("POST", "http://ipman-controller-service.ims.svc/vxlan", bytes.NewBuffer(p))
	fatal(err, "Error preparing reqeust to charon-pod")

	req.Header.Add("Content-Type", "application/json")
	fmt.Println("added header")
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
			fmt.Println("Waiting")
			time.Sleep(time.Duration(vi.Wait * int(time.Second)))
		}

	}

	ipa, subnet, _ := strings.Cut(vxlanip, "/")
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

	xip, _, _ :=  strings.Cut(vi.Xfrm_if_ip, "/")
	fmt.Println("xip: ", xip) // TODO: delete
	ob = ipToByteArray(xip)
	xfrm_ipnet := net.IPNet{
		IP:   net.IPv4(ob[0], ob[1], ob[2], ob[3]),
		Mask: net.CIDRMask(int(sn_int), 32),
	}
	fatal(err, fmt.Sprintf("Error parsing CIDR xfrm_gateway_ip %s", xfrm_gateway))

	vxlan, err := createVxlan(iface, addrs[0].IP, vi.If_id)
	fatal(err, "Error creating vxlan interface")

	err = ip.LinkSetUp(*vxlan)
	fatal(err, "Error settings vxlan interface up")

	ip.AddrAdd(*vxlan, &ip.Addr{IPNet: &vxlan_ipnet})
	cmd := exec.Command("bash", "-c", fmt.Sprintf("bridge fdb append 00:00:00:00:00:00 dev %s dst %s", "vxlan"+strconv.FormatInt(int64(vi.If_id), 10), vi.Xfrm_underlying_ip))
	out, err := cmd.Output()
	if err != nil || string(out) != "" {
		fmt.Println("Error appending to bridge fdb: ", err)
		os.Exit(1)
	}

	fmt.Println("rmeote ts: ", vi.Remote_ts)
	_, remoteipnet, err := net.ParseCIDR(vi.Remote_ts)
	fmt.Println("rmeote ipnet: ", remoteipnet)
	r := &ip.Route{
		LinkIndex: (*vxlan).Attrs().Index,
		Dst:       remoteipnet,
		Src:       vxlan_ipnet.IP,
		Gw:        xfrm_ipnet.IP,
	}
	fmt.Println("r: ", r)
	err = ip.RouteAdd(r)
	if err != nil {
		fmt.Println("Error adding route: ", err)
		cmd := exec.Command("bash", "-c", "while true; do sleep 1000; done;")
		out, err := cmd.Output()
		if err != nil {
			fmt.Println("Error executing command sleep: ", err)
		}
		fmt.Println("Out? lmao: ", string(out))
	}
}
