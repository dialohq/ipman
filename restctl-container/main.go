package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// TODO: put in env variable or something
var (
	SWAN_CONF_PATH = "/etc/swanctl/swanctl.conf"
	CHARON_CONN = "/etc/charon-conn/"
)

type WrongArgumentsError struct{}

func (w *WrongArgumentsError) Error() string {
	return `Amount of arguments and/or their type is invalid.
Valid arguments:
	GET  /status
	GET  /reload
	GET  /init?connName=foo
	GET  /terminate?connName=bar
	GET  /xfrm?pid=61410?ip=10.42.0.xxx
	POST  /vxlan
	`
}

type CommandResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func swanExec(args ...string) *exec.Cmd {
	return exec.Command("swanctl", args...)
}

func getAction(path string, connName *string) (*exec.Cmd, error) {
	if connName == nil {
		switch path {
		case "/status":
			return swanExec("--list-conns"), nil
		case "/reload":
			return swanExec("--load-all", "--file", "/etc/swanctl/swanctl.conf"), nil
		case "/config":
			return exec.Command("cat", "/etc/swanctl/swanctl.conf"), nil
		default:
			return nil, &WrongArgumentsError{}
		}
	}

	// TODO: secure this
	switch path {
	case "/init":
		return swanExec("-i", "-c", *connName), nil
	case "/terminate":
		return swanExec("-t", "-c", *connName), nil
	default:
		return nil, &WrongArgumentsError{}
	}
}

func runCommandHandler(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		fmt.Println("Error parsing form: ", err)
	}

	var splitPath []string
	argsPassed := strings.Contains(r.URL.String(), "?")
	if argsPassed {
		splitPath = strings.Split(r.URL.String(), "?")
	} else {
		splitPath = []string{r.URL.String()}
	}

	path := splitPath[0]
	var connName *string
	if len(splitPath) == 1 {
		connName = nil
	} else {
		connName = &r.Form["connName"][0]
	}

	var resp CommandResponse
	cmd, err := getAction(path, connName)
	if err != nil {
		resp.Error = err.Error()
	} else {
		output, err := cmd.CombinedOutput()
		resp.Output = string(output)
		if err != nil {
			resp.Error = err.Error()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func p0ng(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("p0ng"))
}

var xfrm_cilium_ip *string = nil

type xfrmInfo struct {
	If_id     int64  `json:"if_id"`
	Remote_ts string `json:"remote_ts"`
	Local_ts  string `json:"local_ts"`
	Xfrm_ip   string `json:"xfrm_ip"`
	Vxlan_ip  string `json:"vxlan_ip"`
}

// TODO: clean this up
func createXfrm(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var splitPath []string
	argsPassed := strings.Contains(r.URL.String(), "?")
	if argsPassed {
		splitPath = strings.Split(r.URL.String(), "?")
	} else {
		splitPath = []string{r.URL.String()}
	}
	var resp CommandResponse

	// TODO: dynamically from CR
	// xi := xfrmInfo{
	// 	If_id:     101,
	// 	Remote_ts: "10.0.1.0/24",
	// 	Local_ts:  "10.0.2.0/24",
	// 	Xfrm_ip: "10.0.2.1/24",
	// 	Vxlan_ip: "10.0.2.2/24",
	// }

	w.Header().Set("Content-Type", "application/json")
	// TODO: use int for if_id
	var pid *string
	var ip *string
	var child *string
	if len(splitPath) == 1 {
		pid = nil
		ip = nil
		child = nil
		resp.Error = fmt.Errorf("Not enough arguments error").Error()
		json.NewEncoder(w).Encode(resp)
		return
	}
	pid = &r.Form["pid"][0]
	ip = &r.Form["ip"][0]
	child = &r.Form["child"][0]

	fmt.Println("PID: ", pid)
	fmt.Println("IP: ", ip)
	fmt.Println("CHILD: ", child)
	xfrm_cilium_ip = ip

	cm, err := os.ReadFile(CHARON_CONN + *child)
	if err != nil {
		fmt.Println("Couldn't read file at " + CHARON_CONN + *child)
		writeResponseWait(w, 10)
		return
	}
	fmt.Println("cm: ", string(cm))

	conf := &Child{}
	err = json.Unmarshal(cm, conf)
	if err != nil {
		fmt.Println("Couldn't unmarshal data from file into child struct")
		writeResponseWait(w, 10)
		return
	}
	fmt.Println("child: ", conf)

	xi := xfrmInfo{
		If_id: int64(conf.If_id),
		Remote_ts: conf.RemoteTs,
		Local_ts: conf.LocalTs,
		Xfrm_ip: conf.Xfrm_if_ip,
		Vxlan_ip: conf.Vxlan_if_ip,

	}
	fmt.Println("xi: ", xi)

	if_id := strconv.FormatInt(int64(conf.If_id), 10)
	cmd := exec.Command("bash", "-c", fmt.Sprintf("ip link add xfrm%s type xfrm if_id %s dev ens3", if_id, if_id))
	output, err := cmd.CombinedOutput()
	fmt.Println(string(output))
	if err != nil {
		fmt.Println("Error creating interface: ", err)
		resp.Error = err.Error()

		json.NewEncoder(w).Encode(resp)
		return
	}
	fmt.Println("command to execute: ", fmt.Sprintf("ip link set xfrm%s netns %s", if_id, *pid))
	cmd = exec.Command("bash", "-c", fmt.Sprintf("ip link set xfrm%s netns %s", if_id, *pid))
	output, err = cmd.CombinedOutput()
	fmt.Println(string(output))
	if err != nil {
		fmt.Println("Error moving if to netns with pid: ", err)
		resp.Error = err.Error()
		json.NewEncoder(w).Encode(resp)
		return
	}
	json.NewEncoder(w).Encode(xi)
	return
}

type vxlanInfo struct {
	If_id      int    `json:"vxlan_id,omitempty"`
	Remote_ts  string `json:"remote_ts,omitempty"`
	Xfrm_if_ip string `json:"xfrm_if_ip"`
	Xfrm_underlying_ip string `json:"xfrm_underlying_ip"`
	Wait       int    `json:"wait"` // tells the pod that something is not ready and it should wait n seconds
}

type vxlanData struct {
	Vxlan_default_ip string `json:"vxlan_pod_ip"`
	Child_name string `json:"child_name"`
}

func writeResponseWait(w http.ResponseWriter, t int){
		vi := vxlanInfo{
			Wait: t,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(vi)
	
}

func createVxlan(w http.ResponseWriter, r *http.Request) {
	// TODO: exponential backoff??
	var vi vxlanInfo
	if xfrm_cilium_ip == nil {
		writeResponseWait(w, 10)
		return
	} 

	out, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Println("Error, couldn't read body of request for vxlan: ", err)
		// TODO: respond with something?
		// thats a whole other thing since
		// the vxlan would have to restart itself
		// or something along those lines
		// maybe just let it os.Exit(1)
		return
	}
	var vd vxlanData
	err = json.Unmarshal(out, &vd)
	if err != nil {
		fmt.Println("Couldn't unmarshall body of request for vxlan interface", err)
		writeResponseWait(w, 10)
		return
	}
	cm, err := os.ReadFile(CHARON_CONN + vd.Child_name)
	if err != nil {
		fmt.Println("Couldn't read file at " + CHARON_CONN + vd.Child_name)
		writeResponseWait(w, 10)
		return
	}

	conf := &Child{}
	err = json.Unmarshal(cm, conf)
	if err != nil {
		fmt.Println("Couldn't unmarshal data from file intro child struct")
		writeResponseWait(w, 10)
		return
	}

	vi.If_id = conf.If_id
	vi.Remote_ts = conf.RemoteTs
	vi.Xfrm_if_ip = conf.Xfrm_if_ip
	vi.Xfrm_underlying_ip = *xfrm_cilium_ip
	vi.Wait = 0
	http.Get(fmt.Sprintf("http://%s:8080/add?ip=%s", *xfrm_cilium_ip, vd.Vxlan_default_ip))
	json.NewEncoder(w).Encode(vi)
}

type Child struct {
	Name      string             `json:"name"`
	LocalTs   string             `json:"localTs"`
	RemoteTs  string             `json:"remoteTs"`
	If_id       int              `json:"if_id"`
	Ip_pool     []string         `json:"ip_pool"`
	Xfrm_if_ip  string           `json:"xfrmIfIp"`
	Vxlan_if_ip string           `json:"vxlanIfIp"`
}

func main() {
	http.HandleFunc("/status", runCommandHandler)
	http.HandleFunc("/reload", runCommandHandler)
	http.HandleFunc("/init", runCommandHandler)
	http.HandleFunc("/terminate", runCommandHandler)
	http.HandleFunc("/p1ng", p0ng)
	http.HandleFunc("/xfrm", createXfrm)
	http.HandleFunc("/vxlan", createVxlan)

	log.Println("Listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
