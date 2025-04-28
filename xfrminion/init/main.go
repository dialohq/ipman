package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type CommandResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// var (
// 	if_id int64 = 101
// 	remote_ts = "10.0.1.0/24"
// 	local_ts  = "10.0.2.0/24"
// 	local_subnet = strings.Split(local_ts, "/")[1]

// 	xfrm_if_name = "xfrm" + strconv.FormatInt(if_id, 10)
// 	xfrm_ip   = "10.0.2.1"

// 	vxlan_ip = "10.0.2.2"
// 	vxlan_if_name = "vxlan" + strconv.FormatInt(if_id, 10)
// 	// name of default interface for inter-pod comms
// 	cilium_pod_default_interface = "eth0"
// )

// TODO: this is duplicated in restctl-cont/main.go
type xfrmInfo struct {
	If_id     int64  `json:"if_id"`
	Remote_ts string `json:"remote_ts"`
	Local_ts  string `json:"local_ts"`
}

func getIfAddresses(local_ts string) (string, string, error) {

	address, _, found := strings.Cut(local_ts, "/")
	if !found {
		return "", "", fmt.Errorf("Couldn't find separator ('/')")
	}

	// extract the first 3 octets
	xfrm_ip := strings.Join(strings.Split(address, ".")[0:3], ".")
	vxlan_ip := xfrm_ip // same subnet

	// set the last octet to 1 | 2
	xfrm_ip += ".1"  //+ subnet
	vxlan_ip += ".2" // + subnet

	return xfrm_ip, vxlan_ip, nil
}

func getDefaultInterface() (string, error) {
	cmd := exec.Command("bash", "-c", "ip route")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Error checking routes %s", err)
	}
	output := string(out)

	for line := range strings.SplitSeq(output, "\n") {
		elements := strings.Split(line, " ")
		if elements[0] != "default" {
			continue
		}
		for idx, el := range elements {
			if el == "dev" {
				return elements[idx+1], nil
			}
		}
	}
	return "", fmt.Errorf("Default route not found")
}

func main() {
	cilium_pod_default_interface, err := getDefaultInterface()
	if err != nil {
		fmt.Println("Couldn't find default interface, ", err)
		os.Exit(1)
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf("ip -4 addr show dev %s | grep -oP '(?<=inet\\s)\\d+(\\.\\d+){3}'", cilium_pod_default_interface))
	ipv4bytes, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println("Couldn't fetch ip address of default interface: ", err)
	}
	ipv4 := strings.TrimSuffix(string(ipv4bytes), "\n")
	fmt.Println("Ip identified as: ", ipv4)

	pid := os.Getpid()
	fmt.Println("shell pid: ", pid)
	// TODO: fetch if_id dynamically
	// we could ask for info we need
	// and before we get a response the
	// rest api could move the interface
	// here and then respond with data.
	res, err := http.Get(fmt.Sprintf("http://ipman-controller-service.ims.svc/xfrm?pid=%d&ip=%s", pid, ipv4))
	if err != nil {
		fmt.Println("error while GETting service: ", err)
		os.Exit(1)
	}
	if res.StatusCode != 200 {
		fmt.Println("Status code not 200, exiting. Status code: ", res.StatusCode)
		os.Exit(1)
	}
	defer res.Body.Close()
	r, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println("Error rreading response body")
	}
	var xi xfrmInfo
	err = json.Unmarshal(r, &xi)
	if err != nil {
		fmt.Println("Couldn't unmarshal response from with xfrm interface data: ", err)
		os.Exit(1)
	}

	xfrm_if_name := "xfrm" + strconv.FormatInt(xi.If_id, 10)
	vxlan_if_name := "vxlan" + strconv.FormatInt(xi.If_id, 10)
	subnet := strings.Split(xi.Local_ts, "/")[1]
	xfrm_ip, vxlan_ip, err := getIfAddresses(xi.Local_ts)
	if err != nil {
		fmt.Println("Error extracting interface ip addresses from local_ts, expected x.x.x.x/x, got: ", xi.Local_ts, "\n", err)
		os.Exit(1)
	}

	// TODO: wrap that in a function
	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip addr add %s/%s dev %s", xfrm_ip, subnet, xfrm_if_name))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding ip addr: ", string(out), "\n", err)
	}

	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip link set %s up", xfrm_if_name))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error starting xfrm interface: ", string(out), "\n", err)
	}

	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip route add %s dev %s src %s", xi.Remote_ts, xfrm_if_name, xfrm_ip))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding route retmote ts to xfrm: ", string(out), "\n", err)
	}
	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip route del %s dev %s", xi.Local_ts, xfrm_if_name))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error deleting route: ", string(out), "\n", err)
	}

	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip link add %s type vxlan id %d dstport 4789 dev %s local %s", vxlan_if_name, xi.If_id, cilium_pod_default_interface, ipv4))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding vxlan interface: ", string(out), "\n", err)
	}

	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip addr add %s dev %s", vxlan_ip, vxlan_if_name))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding vxlan interface: ", string(out), "\n", err)
	}

	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip link set %s up", vxlan_if_name))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding vxlan interface: ", string(out), "\n", err)
	}

	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip route add %s dev %s", xi.Local_ts, vxlan_if_name))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding route local_ts to vxlan: ", string(out), "\n", err)
	}

}
