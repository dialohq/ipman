package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"

	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	"dialo.ai/ipman/pkg/netconfig"
	u "dialo.ai/ipman/pkg/utils"
	ip "github.com/vishvananda/netlink"
)

func addEntry(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Couldn't read body of request for bridge fdb append", "msg", err)
		writeError(w, err, nil)
		return
	}

	bfr := &comms.BridgeFdbRequest{}
	err = json.Unmarshal(out, bfr)
	if err != nil {
		logger.Error("Couldn't unmarshal body of request for bridge fdb append", "msg", err)
		writeError(w, err, nil)
		return
	}
	vxlanInterfaceName := "vxlan" + bfr.InterfaceId

	cmd := exec.Command("bash", "-c", fmt.Sprintf("bridge fdb add 00:00:00:00:00:00 dev %s dst %s", vxlanInterfaceName, bfr.CiliumIp))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {

		cmd = exec.Command("bash", "-c", fmt.Sprintf("bridge fdb append 00:00:00:00:00:00 dev %s dst %s", vxlanInterfaceName, bfr.CiliumIp))
		if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
			logger.Error("Error appending to bridge fdb", "msg", err)
			code := 409
			writeError(w, err, &code)
			return
		}
	}

	links, err := ip.LinkList()
	if err != nil {
		logger.Error("Error listing links after bridge fdb append", "msg", err)
		writeError(w, err, nil)
		return
	}

	var link *ip.Link
	for i, l := range links {
		if l.Type() == "vxlan" {
			link = &links[i]
		}
	}

	_, ipnet, err := net.ParseCIDR(bfr.VxlanIp)
	if err != nil {
		logger.Error("Error parsing vxlan ip as CIDR after bridge fdb append", "msg", err)
		writeError(w, err, nil)
		return
	}

	rt := ip.Route{
		LinkIndex: (*link).Attrs().Index,
		Dst:       ipnet,
	}
	err = ip.RouteAdd(&rt)
	if err != nil && err.Error() != "file exists" {
		logger.Error("Error adding route after bridge fdb append", "msg", err)
		writeError(w, err, nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode("OK")
}

func getPid(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	pid := os.Getpid()
	logger.Info("Found PID", "PID", pid)
	w.Header().Set("Content-Type", "application/json")
	rd := comms.PidResponseData{
		Pid: pid,
	}

	out, err := json.Marshal(rd)
	if err != nil {
		logger.Error("Error marshalling request data", "msg", err)
		w.WriteHeader(500)
		_, err := w.Write([]byte{})
		if err != nil {
			logger.Error("Error writing error response", "msg", err)
		}
		return
	}

	w.WriteHeader(200)
	w.Write(out)
}

func writeError(w http.ResponseWriter, err error, status *int) {
	w.Header().Add("Content-Type", "application/json")
	rd := &comms.XfrmResponseData{
		Error: err.Error(),
	}
	if status != nil {
		w.WriteHeader(*status)
	} else {
		w.WriteHeader(400)
	}
	json.NewEncoder(w).Encode(rd)
}

func writeErrorNotNil(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err, nil)
	}
}

func updateRoutes(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	defer r.Body.Close()
	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Error reading body of reqeust for adding routes", "msg", err, "request", *r)
		writeError(w, err, nil)
		return
	}

	localIps := []string{}
	err = json.Unmarshal(out, localIps)
	if err != nil {
		logger.Error("Error unmarshalling body of request for adding routes", "msg", err, "request", *r)
		writeError(w, err, nil)
		return
	}

}

func addRoutes(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	defer r.Body.Close()
	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Error reading body of reqeust for adding routes", "msg", err, "request", *r)
		writeError(w, err, nil)
		return
	}

	child := &ipmanv1.Child{}
	err = json.Unmarshal(out, child)
	if err != nil {
		logger.Error("Error unmarshalling body of request for adding routes", "msg", err, "request", *r)
		writeError(w, err, nil)
		return
	}

	ciliumInterface, err := netconfig.FindDefaultInterface()
	if err != nil {
		logger.Error("Error finding default interface", "msg", err, "request", child)
		writeError(w, err, nil)
		return
	}

	addrs, err := ip.AddrList(*ciliumInterface, ip.FAMILY_V4)
	if err != nil {
		logger.Error("Error listing addresses for default interface", "msg", err, "interface", (*ciliumInterface).Attrs().Name)
		writeError(w, err, nil)
		return
	}

	if len(addrs) == 0 {
		err = fmt.Errorf("List of addresses is empty")
		logger.Error("Error reading list of addresses for default interface (empty)", "interface", (*ciliumInterface).Attrs().Name)
		writeError(w, err, nil)
		return
	}

	ciliumIP := addrs[0].IP

	xfrmInterfaceName := "xfrm" + strconv.FormatInt(int64(child.XfrmIfId), 10)
	vxlanInterfaceName := "vxlan" + strconv.FormatInt(int64(child.XfrmIfId), 10)

	xfrmIp, subnet, found := strings.Cut(child.XfrmIP, "/")
	if !found {
		u.Fatal(
			fmt.Errorf("Couldn't find separator '/'"),
			logger,
			fmt.Sprintf("Error cutting xfrm IP (%s)", child.XfrmIP),
		)
	}

	xfrmIpBytes, err := netconfig.IpToByteArray(xfrmIp)
	if err != nil {
		logger.Error("Error converting ip address string to byte array", "msg", err, "xfrmIP", xfrmIp)
		writeError(w, err, nil)
		return
	}

	i, err := strconv.ParseInt(subnet, 10, 8)
	if err != nil {
		logger.Error("Error parsing subnet from string to int", "subnet", subnet)
		writeError(w, err, nil)
		return
	}
	xfrmIpNet := net.IPNet{
		IP:   net.IPv4(xfrmIpBytes[0], xfrmIpBytes[1], xfrmIpBytes[2], xfrmIpBytes[3]),
		Mask: net.CIDRMask(int(i), 32),
	}

	xfrmIface, err := ip.LinkByName(xfrmInterfaceName)
	u.Fatal(err, logger, "Error getting link by name", "name", xfrmInterfaceName, "msg", err)

	err = ip.AddrAdd(xfrmIface, &ip.Addr{IPNet: &xfrmIpNet})
	u.Fatal(err, logger, "Couldn't add ip address to xfrm interface", "xfrm", xfrmIface, "ip address", xfrmIpNet)

	_, localIpNet, err := net.ParseCIDR(child.XfrmIP)
	u.Fatal(err, logger, "Couldn't parse CIDR of local ts", "local_ts", child.XfrmIP)

	err = ip.LinkSetUp(xfrmIface)
	u.Fatal(err, logger, "Error setting vxlan interface up")

	routes, err := ip.RouteList(xfrmIface, ip.FAMILY_V4)
	u.Fatal(err, logger, "Couldn't list routes for local ip", "dst", localIpNet)

	for i, route := range routes {
		logger.Info("looping through routes", "route", route)
		if route.Dst.IP[0] == localIpNet.IP[0] &&
			route.Dst.IP[1] == localIpNet.IP[1] &&
			route.Dst.IP[2] == localIpNet.IP[2] &&
			route.Dst.IP[3] == localIpNet.IP[3] {
			logger.Info("Found default route", "default", route)
			err = ip.RouteDel(&routes[i])
			u.Fatal(err, logger, "error deleting default route", "route", route)
			break
		}
	}

	var rt *ip.Route
	for _, remote := range child.RemoteIps {
		_, remoteIpNet, err := net.ParseCIDR(remote)
		u.Fatal(err, logger, "Couldn't parse remote ip as CIDR", "remote_ip", remote)
		rt = &ip.Route{
			LinkIndex: xfrmIface.Attrs().Index,
			Dst:       remoteIpNet,
			Src:       xfrmIpNet.IP,
		}
		err = ip.RouteAdd(rt)
		u.Fatal(err, logger, "Couldn't add route to xfrm interface", "route", r, "device", xfrmIface)
	}

	vxlanIfaceTemplate := ip.Vxlan{
		LinkAttrs: ip.LinkAttrs{
			Name:    vxlanInterfaceName,
			NetNsID: -1,
			TxQLen:  -1,
		},
		// we can reuse xfrm iface id
		// as vxlan id
		VxlanId:      int(child.XfrmIfId),
		Port:         4789,
		SrcAddr:      ciliumIP,
		VtepDevIndex: (*ciliumInterface).Attrs().Index,
	}
	err = ip.LinkAdd(&vxlanIfaceTemplate)
	u.Fatal(err, logger, "Couldn't add vxlan interface", "vxlan", vxlanIfaceTemplate)

	vxlanIface, err := ip.LinkByName(vxlanInterfaceName)
	u.Fatal(err, logger, "Couldn't get vxlan interface by name")

	vxlanIpCut, _, found := strings.Cut(child.VxlanIP, "/")
	if !found {
		u.Fatal(
			fmt.Errorf("Couldn't find separator '/'"),
			logger,
			fmt.Sprintf("Error cutting vxlan IP (%s)", xfrmIp),
		)
	}

	IpBytes, err := netconfig.IpToByteArray(vxlanIpCut)
	u.Fatal(err, logger, "Error converting IP of vxlan to byte array")

	vxlanIpNet := net.IPNet{
		IP:   net.IPv4(IpBytes[0], IpBytes[1], IpBytes[2], IpBytes[3]),
		Mask: net.CIDRMask(int(i), 32),
	}
	vxlanIpAddr := ip.Addr{
		IPNet: &vxlanIpNet,
	}
	err = ip.AddrAdd(vxlanIface, &vxlanIpAddr)
	u.Fatal(err, logger, "Couldn't add ip address to interface", "ip", vxlanIpAddr, "iface", vxlanIface)

	err = ip.LinkSetUp(vxlanIface)
	u.Fatal(err, logger, "Error setting vxlan interface up")

	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte{})
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("Ready"))
}

func main() {
	http.HandleFunc("/pid", getPid)
	http.HandleFunc("/addRoutes", addRoutes)
	http.HandleFunc("/addEntry", addEntry)
	http.HandleFunc("/healthz", healthz)

	log.Println("Listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
