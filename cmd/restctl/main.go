package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	"dialo.ai/ipman/pkg/netconfig"
	ip "github.com/vishvananda/netlink"
)

// TODO: put in env variable or something
var (
	SWAN_CONF_PATH = "/etc/swanctl/swanctl.conf"
	CHARON_CONN    = "/etc/charon-conn/"
)

type CommandResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func swanExec(args ...string) *exec.Cmd {
	return exec.Command("swanctl", args...)
}

func getAction(path string, childName *string) (*exec.Cmd, error) {
	if childName == nil {
		switch path {
		case "/status":
			return swanExec("--list-conns"), nil
		case "/reload":
			return swanExec("--load-all", "--file", "/etc/swanctl/swanctl.conf"), nil
		case "/config":
			return exec.Command("cat", "/etc/swanctl/swanctl.conf"), nil
		default:
			return nil, fmt.Errorf("Amount of arguments and/or their type is invalid")
		}
	}

	// TODO: secure this
	switch path {
	case "/init":
		return swanExec("-i", "-c", *childName), nil
	case "/terminate":
		return swanExec("-t", "-c", *childName), nil
	default:
		return nil, fmt.Errorf("Amount of arguments and/or their type is invalid")
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
	fmt.Println("path: ", path)
	var childName *string
	if len(splitPath) == 1 {
		childName = nil
	} else {
		childName = &r.Form["childName"][0]
		fmt.Println("child name: ", *childName)
	}

	var resp CommandResponse
	cmd, err := getAction(path, childName)
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

var xfrmPodCiliumIPs map[string]string = map[string]string{}

// TODO: clean this up
func createXfrm(w http.ResponseWriter, r *http.Request) {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)

	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Couldn't read body of reqeust for vxlan, try again in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}

	var xd comms.XfrmData
	err = json.Unmarshal(out, &xd)
	if err != nil {
		logger.Error("Couldn't unmarshall body of request for xfrm interface, try again in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}
	logger.Info("Received xfrom data", "data", xd)

	w.Header().Set("Content-Type", "application/json")

	xfrmPodCiliumIPs[xd.ChildName] = xd.CiliumIP

	cm, err := os.ReadFile(CHARON_CONN + xd.ChildName)
	if err != nil {
		fmt.Println("Couldn't read file at " + CHARON_CONN + xd.ChildName)
		writeResponseWait(w, 10)
		return
	}

	conf := &ipmanv1.Child{}
	err = json.Unmarshal(cm, conf)
	if err != nil {
		fmt.Println("Couldn't unmarshal data from file into child struct, try agian in 10s")
		writeResponseWait(w, 10)
		return
	}

	xi := comms.XfrmInfo{
		XfrmIfId: int64(conf.XfrmIfId),
		RemoteIps: conf.RemoteIps,
		LocalIps:  conf.LocalIps,
		XfrmIp:   conf.XfrmIP,
		VxlanIp:  conf.VxlanIP,
		Wait:     0,
	}
	fmt.Println("xi:", xi)

	ciliumIface, err := netconfig.FindDefaultInterface()
	if err != nil {
		logger.Error("Couldn't find default interface", "msg", err)
		writeResponseWait(w, 10)
		return
	}
	a := ip.LinkAttrs{
		Name:        "xfrm" + strconv.FormatInt(int64(conf.XfrmIfId), 10),
		NetNsID:     -1,
		TxQLen:      -1,
		Index:       conf.XfrmIfId,
		ParentIndex: (*ciliumIface).Attrs().Index,
	}
	xfrmIface := ip.Xfrmi{
		LinkAttrs: a,
		Ifid:      uint32(conf.XfrmIfId),
	}
	err = ip.LinkAdd(&xfrmIface)
	if err != nil {
		logger.Error("Couldn't create xfrm interface", "msg", err, "interface", xfrmIface)
		writeResponseWait(w, 10)
		return
	}

	err = ip.LinkSetNsPid(&xfrmIface, int(xd.PID))
	if err != nil {
		logger.Error("Couldn't move interface into netns by pid", "interface", xfrmIface, "pid", xd.PID, "msg", err)
		writeResponseWait(w, 10)
		return
	}

	out, err = json.Marshal(xi)
	if err != nil {
		fmt.Println("Couldnt marshall xi", xi)
	}
	fmt.Println("Marshalled xi: ", string(out))
	json.NewEncoder(w).Encode(xi)
	return
}

func writeResponseWait(w http.ResponseWriter, t int) {
	vi := comms.VxlanInfo{
		Wait: t,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vi)

}

func createVxlan(w http.ResponseWriter, r *http.Request) {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)


	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Couldn't read body of reqeust for vxlan, try again in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}

	var vd comms.VxlanData
	err = json.Unmarshal(out, &vd)
	if err != nil {
		logger.Error("Couldn't unmarshall body of request for vxlan interface, try again in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}
	logger.Info("Received vxlan data", "data", vd)

	// TODO: exponential backoff??
	if _, ok := xfrmPodCiliumIPs[vd.ChildName]; !ok{
		logger.Info("Xfrm pods cilium ip is still nil, waiting 10s")
		writeResponseWait(w, 10)
		return
	}

	cm, err := os.ReadFile(CHARON_CONN + vd.ChildName)
	if err != nil {
		logger.Error("Couldn't read file at "+CHARON_CONN+vd.ChildName+" try agian in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}
	logger.Info("Read config map", "cm", cm)

	conf := &ipmanv1.Child{}
	err = json.Unmarshal(cm, conf)
	if err != nil {
		logger.Error("Couldn't unmarshal data from file intro child struct, try agian in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}
	logger.Info("read data from config map", "config", conf)

	var vi comms.VxlanInfo
	vi.IfId = conf.XfrmIfId
	vi.XfrmIP = conf.XfrmIP
	vi.XfrmUnderlyingIP = xfrmPodCiliumIPs[vd.ChildName]
	vi.RemoteIps = conf.RemoteIps
	vi.Wait = 0
	fmt.Println("vd.vxlanifip: ", vd.VxlanIfIp)
	http.Get(fmt.Sprintf("http://%s:8080/add?ip=%s&vxlanIp=%s", xfrmPodCiliumIPs[vd.ChildName], vd.VxlanCiliumIP, vd.VxlanIfIp))
	json.NewEncoder(w).Encode(vi)
}

func main() {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	http.HandleFunc("/status", runCommandHandler)
	http.HandleFunc("/reload", runCommandHandler)
	http.HandleFunc("/config", runCommandHandler)
	http.HandleFunc("/init", runCommandHandler)
	http.HandleFunc("/terminate", runCommandHandler)
	http.HandleFunc("/p1ng", p0ng)
	http.HandleFunc("/xfrm", createXfrm)
	http.HandleFunc("/vxlan", createVxlan)

	logger.Info("Listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Error("Error listening", "msg", err)
	}
}
