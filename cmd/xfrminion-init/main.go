package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net"
	"os"
	"bytes"
	"strconv"
	"strings"
	"time"
	"log/slog"
	"dialo.ai/ipman/pkg/comms"
	"dialo.ai/ipman/pkg/netconfig"
	u "dialo.ai/ipman/pkg/utils"
	ip "github.com/vishvananda/netlink"
)

func main() {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	ciliumInterface, err := netconfig.FindDefaultInterface()
	u.Fatal(err, logger, "Couldn't find default interface")

	addrs, err := ip.AddrList(*ciliumInterface, ip.FAMILY_V4)
	u.Fatal(err, logger, "Error listing addresses")

	if len(addrs) == 0 {
		u.Fatal(fmt.Errorf("List of addresses is empty"),
			logger,
			fmt.Sprintf("Error listing addresses for interface %s", (*ciliumInterface).Attrs().Name))
	}
	logger.Info("Found ip addresses", "addresses", addrs)

	ciliumIP := addrs[0].IP
	logger.Info("Default IP identified", "IP", ciliumIP, "Interface", (*ciliumInterface).Attrs().Name)

	pid := os.Getpid()
	logger.Info("Got shell PID", "pid", pid)
	childName := os.Getenv("CHILD_NAME")
	logger.Info("Got connection child name", "cn", childName)

	xd := comms.XfrmData{
		CiliumIP: ciliumIP.String(),
		PID: int64(pid),
		ChildName: childName,
	}

	p, err := json.Marshal(xd)
	u.Fatal(err, logger, "Error marshaling body of reqeust to charon-pod")
	logger.Info("Marshalled xfrmData", "data", string(p))

	charonAddress := "http://ipman-controller-service.ims.svc/xfrm"
	req, err := http.NewRequest("POST", charonAddress, bytes.NewBuffer(p))
	u.Fatal(err, logger, "Error preparing reqeust to charon-pod")

	req.Header.Add("Content-Type", "application/json")
	client := &http.Client{}
	var xi comms.XfrmInfo
	done := false
	for !done {
		res, err := client.Do(req)
		u.Fatal(err, logger, "Error sending request")

		out, err := io.ReadAll(res.Body)
		u.Fatal(err, logger, "Error reading response body")

		logger.Info("Received response", "response", string(out))
		fmt.Println("Received reponse: ", string(out))

		err = json.Unmarshal(out, &xi)
		u.Fatal(err, logger, "Couldn't unmarshal response from charon-pod")
		if xi.Wait == 0 {
			done = true
		} else {
			logger.Info("Waiting", "Time", xi.Wait)
			time.Sleep(time.Duration(xi.Wait) * time.Second)
		}
		res.Body.Close()
	}

	logger.Info("Received data from charon-pod", "data", xi)
	
	xfrmIfaceName := "xfrm" + strconv.FormatInt(xi.XfrmIfId, 10)
	vxlanIfaceName := "vxlan" + strconv.FormatInt(xi.XfrmIfId, 10)

	// these 2 could probably be chosen at random if not specified
	// but then what happens if user wants to take an ip that vxlan
	// or xfrm has? it could rewire itself in the future, specifying
	// this explicitly is fine for now i think
	xfrmIp := xi.XfrmIp
	vxlanIp := xi.VxlanIp

	xfrmIpAddr, subnetString, found := strings.Cut(xfrmIp, "/")
	if !found {
		u.Fatal(
			fmt.Errorf("Couldn't find separator '/'"),
			logger,
			fmt.Sprintf("Error cutting xfrm IP (%s)", xfrmIp),
		)
	}
	subnetInt, err := strconv.ParseInt(subnetString, 10, 8)
	u.Fatal(err, logger, fmt.Sprintf("Error parsing subnet (%s)", subnetString))

	IpBytes, err := netconfig.IpToByteArray(xfrmIpAddr)
	u.Fatal(err, logger, "Error converting IP of xfrm to byte array")
	xfrmIpNet := net.IPNet{
		IP:   net.IPv4(IpBytes[0], IpBytes[1], IpBytes[2], IpBytes[3]),
		Mask: net.CIDRMask(int(subnetInt), 32),
	}

	xfrmIface, err := ip.LinkByName(xfrmIfaceName)
	u.Fatal(err, logger, "Error getting link by name", "name", xfrmIfaceName, "msg", err)

	err = ip.AddrAdd(xfrmIface, &ip.Addr{IPNet: &xfrmIpNet})
	u.Fatal(err, logger, "Couldn't add ip address to xfrm interface", "xfrm", xfrmIface, "ip address", xfrmIpNet)

	
	_, localIpNet, err := net.ParseCIDR(xi.XfrmIp)
	u.Fatal(err, logger, "Couldn't parse CIDR of local ts", "local_ts", xi.XfrmIp)

	err = ip.LinkSetUp(xfrmIface)
	u.Fatal(err, logger, "Error setting vxlan interface up")

	routes, err := ip.RouteList(xfrmIface, ip.FAMILY_V4)
	u.Fatal(err, logger, "Couldn't list routes for local ip", "dst", localIpNet)
	logger.Info("Got routes", "routes", routes)

	var r *ip.Route
	for i, route := range routes{
		logger.Info("looping through routes", "route", route)
		if route.Dst.IP[0] == localIpNet.IP[0] &&
			route.Dst.IP[1] == localIpNet.IP[1] &&
			route.Dst.IP[2] == localIpNet.IP[2] &&
			route.Dst.IP[3] == localIpNet.IP[3] {
			logger.Info("Found default route", "default", r)
			err = ip.RouteDel(&routes[i])
			u.Fatal(err, logger, "error deleting default route", "route", r)
			break
		}
	}

	for _, remote := range xi.RemoteIps {
		_, remoteIpNet, err := net.ParseCIDR(remote)
		u.Fatal(err, logger, "Couldn't parse remote ip as CIDR", "remote_ip", remote)
		r = &ip.Route{
			LinkIndex: xfrmIface.Attrs().Index,
			Dst: remoteIpNet,
			Src: xfrmIpNet.IP,
		}
		err = ip.RouteAdd(r)
		u.Fatal(err, logger, "Couldn't add route to xfrm interface", "route", r, "device", xfrmIface)
	}

	// TODO: is this necessary?
	// fmt.Println(fmt.Sprintf("ip route del %s dev %s", xi.LocalTs, xfrmIfaceName))
	// cmd = exec.Command("bash", "-c", fmt.Sprintf("ip route del %s dev %s", xi.LocalTs, xfrmIfaceName))
	// if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
	// 	fmt.Println("Error deleting route: ", string(out), "\n", err)
	// }

	vxlanIfaceTemplate := ip.Vxlan{
		LinkAttrs: ip.LinkAttrs{
			Name: vxlanIfaceName,
			NetNsID: -1,
			TxQLen: -1,
		},
		// we can reuse xfrm iface id
		// as vxlan id
		VxlanId: int(xi.XfrmIfId),
		Port: 4789,
		SrcAddr: ciliumIP,
		VtepDevIndex: (*ciliumInterface).Attrs().Index,
	}
	err = ip.LinkAdd(&vxlanIfaceTemplate)
	u.Fatal(err, logger, "Couldn't add vxlan interface", "vxlan", vxlanIfaceTemplate)

	vxlanIface, err := ip.LinkByName(vxlanIfaceName)
	u.Fatal(err, logger, "Couldn't get vxlan interface by name")

	vxlanIpCut, _, found := strings.Cut(vxlanIp, "/")
	if !found {
		u.Fatal(
			fmt.Errorf("Couldn't find separator '/'"),
			logger,
			fmt.Sprintf("Error cutting vxlan IP (%s)", xfrmIp),
		)
	}

	IpBytes, err = netconfig.IpToByteArray(vxlanIpCut)
	u.Fatal(err, logger, "Error converting IP of vxlan to byte array")

	vxlanIpNet := net.IPNet{
		IP:   net.IPv4(IpBytes[0], IpBytes[1], IpBytes[2], IpBytes[3]),
		Mask: net.CIDRMask(int(subnetInt), 32),
	}
	vxlanIpAddr := ip.Addr{
		IPNet: &vxlanIpNet,
	}
	err = ip.AddrAdd(vxlanIface, &vxlanIpAddr)
	u.Fatal(err, logger, "Couldn't add ip address to interface", "ip", vxlanIpAddr, "iface", vxlanIface)

	err = ip.LinkSetUp(vxlanIface)
	u.Fatal(err, logger, "Error setting vxlan interface up")

	// TODO: gets added by default
	// r = &ip.Route{
	// 	LinkIndex: vxlanIface.Attrs().Index,
	// 	Dst: localIpNet,
	// }
	// err = ip.RouteAdd(r)
	// u.Fatal(err, logger, "Error adding route to vxlan", "route", r, "vxlan", vxlanIface)
}
