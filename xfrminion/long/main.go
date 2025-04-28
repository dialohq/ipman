package main

import (
	"fmt"
	// "io"
	"encoding/json"
	"log"
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

var (
	if_id        int64 = 101
	remote_ts          = "10.0.1.0/24"
	local_ts           = "10.0.2.0/24"
	local_subnet       = strings.Split(local_ts, "/")[1]

	xfrm_if_name = "xfrm" + strconv.FormatInt(if_id, 10)
	xfrm_ip      = "10.0.2.1"

	vxlan_ip      = "10.0.2.2"
	vxlan_if_name = "vxlan" + strconv.FormatInt(if_id, 10)
	// name of default interface for inter-pod comms
	cilium_pod_default_interface = "eth0"
)

func addEntry(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var splitPath []string
	argsPassed := strings.Contains(r.URL.String(), "?")
	if argsPassed {
		splitPath = strings.Split(r.URL.String(), "?")
	} else {
		splitPath = []string{r.URL.String()}
	}
	var resp CommandResponse

	w.Header().Set("Content-Type", "application/json")
	// TODO: use int for if_id
	var ip *string
	if len(splitPath) == 1 {
		ip = nil
		resp.Error = fmt.Errorf("Not enough arguments error").Error()
		json.NewEncoder(w).Encode(resp)
		os.Exit(1)
	}
	ip = &r.Form["ip"][0]
	fmt.Println("ip: ", ip)

	cmd := exec.Command("bash", "-c", fmt.Sprintf("bridge fdb add 00:00:00:00:00:00 dev %s dst %s", vxlan_if_name, *ip))
	if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
		fmt.Println("Error adding bridge fdb entry: ", string(out), "\n", err)

		fmt.Println("Trying to append...")
		cmd = exec.Command("bash", "-c", fmt.Sprintf("bridge fdb append 00:00:00:00:00:00 dev %s dst %s", vxlan_if_name, *ip))
		if out, err := cmd.CombinedOutput(); string(out) != "" || err != nil {
			fmt.Println("Couldn't append. Exiting")

			resp.Error = err.Error()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			os.Exit(1)
		}
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
