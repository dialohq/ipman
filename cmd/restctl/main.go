package main

import (
	"encoding/json"
	"flag"
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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ip "github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
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

func normalizeTime(time string) (int, error) {
	if time[len(time)-1] == 'm' {
		timeNum, err := strconv.ParseInt(time[:len(time)-1], 10, 64)
		if err != nil {
			return 0, err
		}
		return int(timeNum) * 60, nil
	}
	if time[len(time)-1] == 'h' {
		timeNum, err := strconv.ParseInt(time[:len(time)-1], 10, 64)
		if err != nil {
			return 0, err
		}
		return int(timeNum) * 60 * 60, nil
	}
	time = strings.TrimRight(time, "s")
	n, err := strconv.ParseInt(time, 10, 64)
	return int(n), err
}

func translate(ipsec ipmanv1.IPSecConnectionSpec) (*goviciclient.IKEConfig, error) {
	version, err := strconv.ParseInt(getExtra(ipsec.Extra, "version", "2"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("Error parsing version: %w", err)
	}

	ike := goviciclient.IKEConfig{
		LocalAddrs:  []string{ipsec.LocalAddr},
		RemoteAddrs: []string{ipsec.RemoteAddr},
		Version:     strconv.FormatInt(version, 10),
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

	// Handle proposals - default or from Extra
	if val, ok := ipsec.Extra["proposals"]; ok && val != "" {
		ike.Proposals = []string{ipsec.Extra["proposals"]}
	} else {
		// Default proposals if not specified
		ike.Proposals = []string{"aes256-sha256-ecp256"}
	}

	// Handle rekey_time and reauth_time
	if val, ok := ipsec.Extra["rekey_time"]; ok {
		rekeyTime, err := normalizeTime(val)
		if err != nil {
			return nil, fmt.Errorf("Error parsing rekey_time: %w", err)
		}
		ike.RekeyTime = rekeyTime
	} else {
		// Default rekey_time if not specified (4h = 14400s)
		ike.RekeyTime = 14400
	}

	if val, ok := ipsec.Extra["reauth_time"]; ok {
		reauthTime, err := normalizeTime(val)
		if err != nil {
			return nil, fmt.Errorf("Error parsing reauth_time: %w", err)
		}
		ike.ReauthTime = reauthTime
	} else {
		// Default reauth_time if not specified (4h = 14400s)
		ike.ReauthTime = 14400
	}

	// Process connection-level options from Extra
	if val, ok := ipsec.Extra["local_port"]; ok {
		ike.LocalPort = val
	}
	if val, ok := ipsec.Extra["remote_port"]; ok {
		ike.RemotePort = val
	}
	if val, ok := ipsec.Extra["pull"]; ok {
		ike.Pull = val
	}
	if val, ok := ipsec.Extra["dscp"]; ok {
		ike.Dscp = val
	}
	if val, ok := ipsec.Extra["encap"]; ok {
		ike.Encap = val
	}
	if val, ok := ipsec.Extra["mobike"]; ok {
		ike.Mobike = val
	}
	if val, ok := ipsec.Extra["dpd_delay"]; ok {
		ike.DpdDelay = val
	}
	if val, ok := ipsec.Extra["dpd_timeout"]; ok {
		ike.DpdTimeout = val
	}
	if val, ok := ipsec.Extra["fragmentation"]; ok {
		ike.Fragmentation = val
	}
	if val, ok := ipsec.Extra["childless"]; ok {
		ike.Childless = val
	}
	if val, ok := ipsec.Extra["send_certreq"]; ok {
		ike.SendCertreq = val
	}
	if val, ok := ipsec.Extra["send_cert"]; ok {
		ike.SendCert = val
	}
	if val, ok := ipsec.Extra["ppk_id"]; ok {
		ike.PpkId = val
	}
	if val, ok := ipsec.Extra["ppk_required"]; ok {
		ike.PpkRequired = val
	}
	if val, ok := ipsec.Extra["keyingtries"]; ok {
		ike.Keyingtries = val
	}
	if val, ok := ipsec.Extra["unique"]; ok {
		ike.Unique = val
	}
	if val, ok := ipsec.Extra["over_time"]; ok {
		ike.OverTime = val
	}
	if val, ok := ipsec.Extra["rand_time"]; ok {
		ike.RandTime = val
	}
	if val, ok := ipsec.Extra["mediation"]; ok {
		ike.Mediation = val
	}
	if val, ok := ipsec.Extra["mediated_by"]; ok {
		ike.MediatedBy = val
	}
	if val, ok := ipsec.Extra["mediation_peer"]; ok {
		ike.MediationPeer = val
	}
	if val, ok := ipsec.Extra["close_action"]; ok {
		ike.CloseAction = val
	}

	// Handle pools separately as it's an array
	if poolsStr, ok := ipsec.Extra["pools"]; ok && poolsStr != "" {
		ike.Pools = strings.Split(poolsStr, ",")
	}

	for k, v := range ipsec.Children {
		child := goviciclient.ChildSAConfig{
			LocalTS:        v.LocalIPs,
			RemoteTS:       v.RemoteIPs,
			InInterfaceID:  v.XfrmIfId,
			OutInterfaceID: v.XfrmIfId,
		}

		// Handle child mode - default to "tunnel" if not specified
		mode, ok := v.Extra["mode"]
		if ok {
			child.Mode = mode
		} else {
			child.Mode = "tunnel"
		}

		// Process child options
		if val, ok := v.Extra["start_action"]; ok {
			child.StartAction = val
		}
		if val, ok := v.Extra["esp_proposals"]; ok {
			child.EspProposals = []string{val}
		}
		if val, ok := v.Extra["rekey_time"]; ok {
			child.RekeyTime = val
		}
		if val, ok := v.Extra["rand_packets"]; ok {
			child.RandPackets = val
		}
		if val, ok := v.Extra["updown"]; ok {
			child.Updown = val
		}
		if val, ok := v.Extra["hostaccess"]; ok {
			child.Hostaccess = val
		}
		if val, ok := v.Extra["policies"]; ok {
			child.Policies = val
		}
		if val, ok := v.Extra["set_mark_in"]; ok {
			child.SetMarkIn = val
		}
		if val, ok := v.Extra["set_mark_out"]; ok {
			child.SetMarkOut = val
		}
		if val, ok := v.Extra["dpd_action"]; ok {
			child.DpdAction = val
		}
		if val, ok := v.Extra["close_action"]; ok {
			child.CloseAction = val
		}

		// Store any remaining extras that aren't explicitly handled
		for key, value := range v.Extra {
			switch key {
			case "mode", "start_action", "esp_proposals", "rekey_time", "rand_packets",
				"updown", "hostaccess", "policies", "set_mark_in", "set_mark_out",
				"dpd_action", "close_action":
				// Skip options that are already handled
				continue
			default:
				// Add to Extra map for any other options
				if child.Extra == nil {
					child.Extra = make(map[string]string)
				}
				child.Extra[key] = value
			}
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
		logger.Error("Couldn't read body of reqeust for xfrm, try again in 10s", "msg", err)
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

	vc, err := goviciclient.NewViciClient(nil)
	if err != nil {
		response.Errs = append(response.Errs, fmt.Sprintf("Failed to create vici client: %w", err))
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(response)
		return
	}
	defer vc.Close()

	data := &comms.ReloadData{}
	err = json.Unmarshal(dataBytes, data)
	if err != nil {
		w.WriteHeader(400)
		response.Errs = append(response.Errs, "Failed to unmarshal request data")
		json.NewEncoder(w).Encode(response)
		return
	}
	if data.Configs == nil {
		w.WriteHeader(400)
		response.Errs = append(response.Errs, "Config slice is nil")
		json.NewEncoder(w).Encode(response)
		return
	}
	wantedConfigNames := make([]string, len(data.Configs)*2)
	for _, cfg := range data.Configs {
		wantedConfigNames = append(wantedConfigNames, cfg.IPSecConnection.Name)
		wantedConfigNames = append(wantedConfigNames, cfg.IPSecConnection.Spec.Name)
	}
	wantedChildrenNames := make([]string, len(data.Configs)*2)
	for _, cfg := range data.Configs {
		for childName := range cfg.IPSecConnection.Spec.Children {
			wantedChildrenNames = append(wantedChildrenNames, childName)
			wantedChildrenNames = append(wantedChildrenNames, childName)
		}
	}

	loadedConns, err := vc.ListConns(nil)
	for _, confMap := range loadedConns {
		for name := range confMap {
			for _, conf := range data.Configs {
				if !slices.Contains(wantedConfigNames, name) {
					fmt.Println("Unloading conn: ", name)
					err = vc.UnloadConns(name)
					if err != nil {
						fmt.Println("Err unloading conn: ", err)
					}
				} else {
					fmt.Println("------------")
					fmt.Println("Not deleting config ", name)
					fmt.Println("From list")
					for _, confii := range data.Configs {
						fmt.Println(confii.IPSecConnection.Name)
					}
					fmt.Sprintf("%s == %s %t", name, conf.IPSecConnection.Name, name == conf.IPSecConnection.Name)
					fmt.Println("------------")
				}
			}
		}
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
		conns, err := vc.ListConns(nil)
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

type PromLogger struct {
}

func (l PromLogger) Println(v ...any) {
	klog.V(5).Info(v...)
}

func main() {
	klog.InitFlags(nil)
	flag.Set("v", "5")
	flag.Parse()
	klog.Info("klogging")
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	SOCKETS_DIR := os.Getenv("HOST_SOCKETS_PATH")
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

	go func() {
		for {
			var vc *goviciclient.ViciClient
			for {
				vc, err = goviciclient.NewViciClient(nil)
				if err != nil {
					logger.Error("Failed to create a vici client...", "msg", err)
					time.Sleep(time.Second / 5)
					continue
				}
				break
			}
			time.Sleep(5 * time.Second)
			tryInit(vc, logger)
			err = vc.Close()
			if err != nil {
				logger.Error("Failed to close a vici client", "msg", err)
			}
		}
	}()

	mux := http.NewServeMux()

	strongswanCollector := NewStrongswanCollector()
	strongswanCollector.init()
	pl := PromLogger{}
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		ErrorLog: pl,
	}))
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
