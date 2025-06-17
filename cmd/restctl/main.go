package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/pkg/comms"
	"dialo.ai/ipman/pkg/netconfig"
	"dialo.ai/ipman/pkg/swanparse"
	"github.com/fsnotify/fsnotify"
	"github.com/plan9better/goviciclient"
	ip "github.com/vishvananda/netlink"
)

var (
	STRONGSWAN_CONF_PATH = "/etc/strongswan.conf"
	SWANCTL_CONF_PATH    = ipmanv1.CharonConfVolumeMountPath + "swanctl.conf"
	CHARON_CONN          = ipmanv1.CharonConnVolumeMountPath
)

type CommandResponse struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func getExtra(e map[string]string, k string, d string) string {
	val, ok := e[k]
	if ok {
		return val
	}
	return d
}

func translate(ipsec ipmanv1.IPSecConnectionSpec) (*goviciclient.IKEConfig, error) {
	// parse from extra
	rekey_time, err := strconv.ParseInt(getExtra(ipsec.Extra, "rekey_time", "14400"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("Error parsing rekey_time: %w", err)
	}
	reauth_time, err := strconv.ParseInt(getExtra(ipsec.Extra, "reauth_time", "14400"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("Error parsing reauth_time: %w", err)
	}
	proposals := getExtra(ipsec.Extra, "esp_proposals", "aes256-sha256-ecp256")
	version, err := strconv.ParseInt(getExtra(ipsec.Extra, "version", "2"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("Error parsing proposals: %w", err)
	}

	ike := goviciclient.IKEConfig{
		LocalAddrs:  []string{ipsec.LocalAddr},
		RemoteAddrs: []string{ipsec.RemoteAddr},
		Proposals:   []string{proposals},
		Version:     strconv.FormatInt(version, 10),
		ReauthTime:  int(rekey_time),
		RekeyTime:   int(reauth_time),
		LocalAuths: &goviciclient.LocalAuthConfig{
			ID:   ipsec.LocalId,
			Auth: "psk",
		},
		RemoteAuths: &goviciclient.RemoteAuthConfig{
			ID:   ipsec.RemoteId,
			Auth: "psk",
		},
		Children: map[string]goviciclient.ChildSAConfig{},
	}

	for k, v := range ipsec.Children {
		child := goviciclient.ChildSAConfig{
			LocalTS:        v.LocalIPs,
			RemoteTS:       v.RemoteIPs,
			InInterfaceID:  v.XfrmIfId,
			OutInterfaceID: v.XfrmIfId,
		}
		val, ok := v.Extra["start_action"]
		if ok {
			child.StartAction = val
		}
		val, ok = v.Extra["esp_proposals"]
		if ok {
			child.EspProposals = []string{val}
		}

		ike.Children[k] = child
	}
	return &ike, nil
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
	logger.Info("Adding xfrm interface")

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
			logger.Error("No link with that name found", "msg", err, "link", l)
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
	if _, ok := xfrmPodCiliumIPs[vd.ChildName]; !ok {
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
	vi.RemoteIps = conf.RemoteIPs
	vi.Wait = 0
	http.Get(fmt.Sprintf("http://%s:8080/add?ip=%s&vxlanIp=%s", xfrmPodCiliumIPs[vd.ChildName], vd.VxlanCiliumIP, vd.VxlanIfIp))
	logger.Info("Added vxlan interface", "id", vi.IfId)
	json.NewEncoder(w).Encode(vi)
}

func swanctl(args ...string) (string, error) {
	cmd := exec.Command("swanctl", args...)
	out, err := cmd.Output()
	return string(out), err
}

func reloadConfig(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)
	logger.Info("Reloading config")

	dataBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Error reading body of request for reload", "msg", err.Error())
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}
	response := comms.ConnectionLoadError{
		FailedConns:   []string{},
		FailedSecrets: []string{},
		Errs:          []string{},
	}

	data := &comms.ReloadData{}
	err = json.Unmarshal(dataBytes, data)
	if err != nil {
		w.WriteHeader(400)
		response.Errs = append(response.Errs, "Failed to unmarshal request data")
		json.NewEncoder(w).Encode(response)
		return
	}
	type secretInfo struct {
		secret string
		id     string
	}
	toLoad := map[string]goviciclient.IKEConfig{}
	secrets := map[string]secretInfo{}
	for _, c := range data.Configs {
		cfg, err := translate(c.IPSecConnection.Spec)
		if err != nil {
			response.Errs = append(response.Errs, fmt.Sprintf("Failed to parse config '%s': %w", c.IPSecConnection.Spec.Name, err))
		} else {
			toLoad[c.IPSecConnection.Spec.Name] = *cfg
			secrets[c.IPSecConnection.Spec.Name] = secretInfo{secret: c.Secret, id: c.IPSecConnection.Spec.RemoteAddr}
		}
	}
	vc, err := goviciclient.NewViciClient("", "")
	if err != nil {
		response.Errs = append(response.Errs, fmt.Sprintf("Failed to create vici client: %w", err))
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(response)
		return
	}
	defer vc.Close()

	notLoadedSecrets := []string{}
	for k, secret := range secrets {
		ke := goviciclient.Key{
			Typ:    "IKE",
			Data:   strings.TrimSpace(secret.secret),
			Owners: []string{secret.id},
		}
		err = vc.LoadShared(ke)
		if err != nil {
			notLoadedSecrets = append(notLoadedSecrets, k)
		}
	}
	for _, k := range notLoadedSecrets {
		delete(toLoad, k)
	}

	err = vc.LoadConns(toLoad)
	notLoadedConns := []string{}
	if err != nil {
		conns, err := vc.ListConns("")
		if err != nil {
			response.Errs = append(response.Errs, fmt.Sprintf("Some configs failed to load, Couldn't check which ones: %s", err.Error()))
		} else {
			loaded := []string{}
			for _, con := range conns {
				loaded = append(loaded, slices.Collect(maps.Keys(con))...)
			}

			for name := range maps.Keys(toLoad) {
				if !slices.Contains(loaded, name) {
					notLoadedConns = append(notLoadedConns, name)
				}
			}
		}
	}

	if len(notLoadedConns) != 0 || len(notLoadedSecrets) != 0 || len(response.Errs) != 0 {
		w.WriteHeader(400)
	} else {
		w.WriteHeader(200)
	}
	response.FailedConns = notLoadedConns
	response.FailedSecrets = notLoadedSecrets
	json.NewEncoder(w).Encode(response)
	return
}

// RestartConnection attempts to restart a connection
func RestartConnection(connName string, logger *slog.Logger) error {
	output, err := swanctl("--initiate", "--child", connName, "--timeout", "3")
	if err != nil {
		logger.Error("Failed to restart connection", "conn", connName, "err", err, "output", output)
		return fmt.Errorf("failed to restart connection %s: %w", connName, err)
	}
	logger.Info("Successfully initiated connection", "conn", connName, "output", output)
	return nil
}

func reconcileIPSec(conns, sas *swanparse.SwanAST) error {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)

	reconciler := NewReconcileVisitor(conns, sas)
	err := reconciler.VisitAST(conns)
	if err != nil {
		return err
	}

	// Handle missing connections and children
	if len(reconciler.MissingConns) > 0 || len(reconciler.MissingChildren) > 0 {
		logger.Info("Found missing connections or children",
			"missing_conns", slices.Collect(maps.Keys(reconciler.MissingConns)),
			"missing_children", slices.Collect(maps.Keys(reconciler.MissingChildren)))

		// Restart missing connections
		for conn := range reconciler.MissingConns {
			if err := RestartConnection(conn, logger); err != nil {
				logger.Error("Error restarting connection", "conn", conn, "err", err)
			} else {
				logger.Info("Successfully restarted connection", "conn", conn)
			}
		}

		// Restart missing child connections
		for child := range reconciler.MissingChildren {
			if err := RestartConnection(child, logger); err != nil {
				logger.Error("Error restarting child connection", "child", child, "err", err)
			} else {
				logger.Info("Successfully restarted child connection", "child", child)
			}
		}
	}

	return nil
}

func tryInit(vc *goviciclient.ViciClient, logger *slog.Logger) {
	// Get the list of connections
	o, err := swanctl("--list-conns", "--raw")
	if err != nil {
		logger.Error("Error listing swanctl connections", "err", err)
		return
	}
	connsAST, err := swanparse.Parse(o)
	if err != nil {
		logger.Error("Error parsing swanctl connections", "err", err)
		return
	}

	// Get the list of security associations
	o, err = swanctl("--list-sas", "--raw")
	if err != nil {
		logger.Error("Error listing security associations", "err", err)
		return
	}
	sasAST, err := swanparse.Parse(o)
	if err != nil {
		logger.Error("Error parsing security associations", "err", err)
		return
	}

	// Find missing connections and restart them
	err = reconcileIPSec(connsAST, sasAST)
	if err != nil {
		logger.Error("Error reconciling connections", "err", err)
		return
	}
}

func fileExists(path string) (bool, error) {
	path = strings.TrimSuffix(path, "/")
	p := strings.Split(path, "/")
	if p == nil {
		return false, fmt.Errorf("Invalid path")
	}
	fileName := p[len(p)-1]
	dirPath := p[:len(p)-1]

	ents, err := os.ReadDir(strings.Join(dirPath, "/"))
	if err != nil {
		return false, err
	}
	for _, e := range ents {
		if e.Name() == fileName {
			return true, nil
		}
	}
	return false, nil
}

func main() {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	SOCKETS_DIR := os.Getenv("HOST_SOCKETS_PATH")
	fmt.Println(len(SOCKETS_DIR), []rune(SOCKETS_DIR))
	if !strings.HasSuffix(SOCKETS_DIR, "/") {
		SOCKETS_DIR = SOCKETS_DIR + "/"
	}
	ViciSocketPath := SOCKETS_DIR + "charon.vici"
	RestctlSocketPath := SOCKETS_DIR + "restctl.sock"

	ok, err := fileExists(ViciSocketPath)
	if err != nil {
		logger.Error("Error while checking for socket existence.")
		os.Exit(1)
	}

	if !ok {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			logger.Error("Error creating fs watcher")
			os.Exit(1)
		}
		err = fsw.Add(SOCKETS_DIR)
		if err != nil {
			logger.Error("Couldn't add a path to watch", "msg", err, "path", ViciSocketPath)
		}
		for {
			e := <-fsw.Events
			if e.Name == ViciSocketPath && e.Has(fsnotify.Create) {
				break
			}
		}
	}

	var vc *goviciclient.ViciClient
	for {
		vc, err = goviciclient.NewViciClient("", "")
		if err != nil {
			logger.Error("Failed to create a vici client on container start", "msg", err)
			time.Sleep(time.Second)
			continue
		} else {
			logger.Info("Created go vici client")
			break
		}
	}
	go func() {
		for {
			time.Sleep(5 * time.Second)
			tryInit(vc, logger)
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/p1ng", p0ng)
	mux.HandleFunc("/reload", reloadConfig)
	mux.HandleFunc("/xfrm", createXfrm)
	mux.HandleFunc("/vxlan", createVxlan)

	server := http.Server{
		Handler: mux,
	}

	ok, err = fileExists(RestctlSocketPath)
	if err != nil {
		logger.Error("Error while checking for restctl socket existence.")
		os.Exit(1)
	}
	if ok {
		err = os.Remove(RestctlSocketPath)
		if err != nil {
			fmt.Println("Couldn't remove already existing restctl socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", RestctlSocketPath)
	if err != nil {
		logger.Error("Error listening on socket", "msg", err, "socket-path", RestctlSocketPath)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-c
		os.Remove(RestctlSocketPath)
		os.Exit(0)
	}()

	logger.Info("Listening on socket", "socket", RestctlSocketPath)
	if err := server.Serve(listener); err != nil {
		logger.Error("Couldn't start server on listener", "msg", err)
	}
}
