package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"

	ip "github.com/vishvananda/netlink"
)

type CommandResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func addEntry(w http.ResponseWriter, r *http.Request) {
	ifid := os.Getenv("IF_ID")
	vxlan_if_name := "vxlan" + ifid

	r.ParseForm()
	var splitPath []string
	argsPassed := strings.Contains(r.URL.String(), "?")
	if argsPassed {
		splitPath = strings.Split(r.URL.String(), "?")
	} else {
		splitPath = []string{r.URL.String()}
	}
	var resp CommandResponse
	fmt.Println("Splitpath: ", splitPath)

	w.Header().Set("Content-Type", "application/json")
	var ciliumIp *string
	var vxlanIP *string
	if len(splitPath) == 1 {
		ciliumIp = nil
		vxlanIP = nil
		resp.Error = fmt.Errorf("Not enough arguments error").Error()
		json.NewEncoder(w).Encode(resp)
		os.Exit(1)
	}
	fmt.Println(r.Form)
	ciliumIp = &r.Form["ip"][0]
	fmt.Println("ip: ", ciliumIp)
	vxlanIP = &r.Form["vxlanIp"][0]

	cmd := exec.Command("bash", "-c", fmt.Sprintf("bridge fdb add 00:00:00:00:00:00 dev %s dst %s", vxlan_if_name, *ciliumIp))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding bridge fdb entry: ", string(out), "\n", err)

		fmt.Println("Trying to append...")
		cmd = exec.Command("bash", "-c", fmt.Sprintf("bridge fdb append 00:00:00:00:00:00 dev %s dst %s", vxlan_if_name, *ciliumIp))
		if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
			fmt.Println("Couldn't append. Exiting")

			resp.Error = err.Error()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			os.Exit(1)
		}
	}

	links, err := ip.LinkList()
	if err != nil {
		fmt.Println("Error listing links", "msg", err)
		resp.Error = err.Error()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
	var link *ip.Link
	for i, l := range links {
		if l.Type() == "vxlan" {
			link = &links[i]
		}
	}

	_, ipnet, err := net.ParseCIDR(*vxlanIP)
	if err != nil {
		fmt.Println("Couldn't parse ip as cidr: ", vxlanIP, err)
		resp.Error = err.Error()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
	rt := ip.Route{
		LinkIndex: (*link).Attrs().Index,
		Dst: ipnet,
	}
	err = ip.RouteAdd(&rt)
	if err != nil {
		fmt.Println("couldn't add route to vxlan","route", rt, err)
		resp.Error = err.Error()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	resp.Output = "OK"
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/add", addEntry)

	log.Println("Listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
