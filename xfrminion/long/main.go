package main

import (
	"fmt"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
