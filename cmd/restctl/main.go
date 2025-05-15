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

func createXfrm(w http.ResponseWriter, r *http.Request) {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)
	w.Header().Set("Content-Type", "application/json")

	out, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Couldn't read body of reqeust for vxlan, try again in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}

	xrd := &comms.XfrmRequestData{}
	err = json.Unmarshal(out, xrd)
	if err != nil {
		logger.Error("Couldn't unmarshal body of request for xfrm", "msg", err)
		writeResponseWait(w, 10)
		return
	}

	ciliumIface, err := netconfig.FindDefaultInterface()
	if err != nil {
		logger.Error("Couldn't find default interface", "msg", err)
		writeResponseWait(w, 10)
		return
	}
	linkName := "xfrm" + strconv.FormatInt(int64(xrd.XfrmIfId), 10)
	a := ip.LinkAttrs{
		Name:        linkName,
		NetNsID:     -1,
		TxQLen:      -1,
		ParentIndex: (*ciliumIface).Attrs().Index,
	}
	xfrmIface := ip.Xfrmi{
		LinkAttrs: a,
		Ifid:      uint32(xrd.XfrmIfId),
	}
	err = ip.LinkAdd(&xfrmIface)
	if err != nil {
		logger.Info("Error adding link, trying to delete conflicting one", "msg", err)
		l, err := ip.LinkByName(linkName)
		if err != nil {
			logger.Error("No link with that name found","msg" , err, "link", l)
		}

		err = ip.LinkDel(l)
		if err != nil {
			logger.Error("Couldn't delete conflicting interface", "msg", err, "interface", xfrmIface)
			writeResponseWait(w, 10)
			return
		}

		err = ip.LinkAdd(&xfrmIface)
		if err != nil {
			logger.Error("Couldn't add interface after deletion", "msg", err, "interface", xfrmIface)
			writeResponseWait(w, 10)
			return
		}
	}

	err = ip.LinkSetNsPid(&xfrmIface, int(xrd.PID))
	if err != nil {
		logger.Error("Couldn't move interface into netns by pid", "interface", xfrmIface, "pid", xrd.PID, "msg", err)
		writeResponseWait(w, 10)
		return
	}

	logger.Info("Added xfrm interface", "name", xfrmIface.Attrs().Name)
	json.NewEncoder(w).Encode(comms.XfrmResponseData{Error: ""})
	return
}

func writeResponseWait(w http.ResponseWriter, t int) {
	// TODO: vxlan and xfrm structs share it but
	// something custom would be better
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

	conf := &ipmanv1.Child{}
	err = json.Unmarshal(cm, conf)
	if err != nil {
		logger.Error("Couldn't unmarshal data from file intro child struct, try agian in 10s", "msg", err)
		writeResponseWait(w, 10)
		return
	}

	var vi comms.VxlanInfo
	vi.IfId = conf.XfrmIfId
	vi.XfrmIP = conf.XfrmIP
	vi.XfrmUnderlyingIP = xfrmPodCiliumIPs[vd.ChildName]
	vi.RemoteIps = conf.RemoteIps
	vi.Wait = 0
	http.Get(fmt.Sprintf("http://%s:8080/add?ip=%s&vxlanIp=%s", xfrmPodCiliumIPs[vd.ChildName], vd.VxlanCiliumIP, vd.VxlanIfIp))
	logger.Info("Added vxlan interface", "id", vi.IfId)
	json.NewEncoder(w).Encode(vi)
}

func reloadConfig(w http.ResponseWriter, r *http.Request){
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	dataBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error(err.Error(), "Error reading body of request for reload")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}
	
	data := &comms.ReloadData{}
	err = json.Unmarshal(dataBytes, data)
	if err != nil {
		logger.Error("Error unmarshalling data of request for reload", "msg", err.Error(), "request", string(dataBytes))
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}

	err = os.WriteFile(SWAN_CONF_PATH, []byte(data.SerializedConfig), 0644)
	if err != nil {
		logger.Error(err.Error(), "Error writing data of request for reload to file")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}

	logger.Info("Reloading swanctl")
	cmd := swanExec("--load-all", "--file", SWAN_CONF_PATH)
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("Couldn't reload swanctl","output", string(out),"error", err)
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}

	w.WriteHeader(200)
	w.Write([]byte("OK"))
}

func main() {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	http.HandleFunc("/status", runCommandHandler)
	http.HandleFunc("/config", runCommandHandler)
	http.HandleFunc("/init", runCommandHandler)
	http.HandleFunc("/terminate", runCommandHandler)
	http.HandleFunc("/p1ng", p0ng)
	http.HandleFunc("/reload", reloadConfig)
	http.HandleFunc("/xfrm", createXfrm)
	http.HandleFunc("/vxlan", createVxlan)

	logger.Info("Listening on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.Error("Error listening", "msg", err)
	}
}
