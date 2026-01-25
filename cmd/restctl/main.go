package main

import (
	"context"
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
	"dialo.ai/ipman/pkg/swanparse"
	"github.com/fsnotify/fsnotify"
	"github.com/plan9better/goviciclient"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

type FailureInfo struct {
	Connection string
	Child      string
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

	// TODO we need to do better than this 😞
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

func swanctl(args ...string) (string, error) {
	cmd := exec.Command("swanctl", args...)
	out, err := cmd.Output()
	return string(out), err
}
func swanctlCombinedOutput(args ...string) (string, error) {
	cmd := exec.Command("swanctl", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getConfigs(w http.ResponseWriter, r *http.Request) {
	conns, err := swanctlListConns()
	if err != nil {
		json.NewEncoder(w).Encode(comms.ConfigRequest{
			Error: err,
		})
		return
	}

	sas, err := swanctlListSas()
	if err != nil {
		json.NewEncoder(w).Encode(comms.ConfigRequest{
			Error: err,
		})
		return
	}
	v := NewStrongSwanVisitor(conns, sas)

	json.NewEncoder(w).Encode(comms.ConfigRequest{
		Conns: slices.Collect(maps.Keys(v.SasChildren)),
		Error: nil,
	})
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

	MAX_TRIES := 30
	tries := 0
	var vc *goviciclient.ViciClient
	for tries < MAX_TRIES {
		vc, err = goviciclient.NewViciClient(nil)
		if err == nil && vc != nil {
			break
		}
		time.Sleep(time.Second / 10)
	}
	if vc == nil {
		response.Errs = append(response.Errs, fmt.Sprintf("Failed to create vici client: %s", err.Error()))
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(response)
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
			for range data.Configs {
				if !slices.Contains(wantedConfigNames, name) {
					fmt.Println("Unloading conn: ", name)
					err = vc.UnloadConns(name)
					if err != nil {
						fmt.Println("Err unloading conn: ", err)
					}
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
			response.Errs = append(response.Errs, fmt.Sprintf("Failed to parse config '%s': %s", c.IPSecConnection.Spec.Name, err.Error()))
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

			for name := range toLoad {
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
}

// RestartConnection attempts to restart a connection
func RestartConnection(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	dataBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Error reading body of request for connection restart", "msg", err.Error())
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}

	data := &comms.RestartConnectionRequest{}
	err = json.Unmarshal(dataBytes, data)
	if err != nil {
		logger.Error("Error unmarshaling body of request for a connection restart", "msg", err.Error())
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}
	logger.Info("Restarting connection", "connection", data.Name)

	output, err := swanctlCombinedOutput("--initiate", "--ike", data.Name, "--timeout", "3")
	if err != nil {
		logger.Error("Failed to restart connection", "conn", data.Name, "err", err, "output", output)
		w.WriteHeader(500)
		json.NewEncoder(w).Encode("Action failed")
		return
	}
	logger.Info("Successfully initiated connection", "conn", data.Name, "output", output)
	w.WriteHeader(200)
	json.NewEncoder(w).Encode("OK")
}

func RestartConnectionChild(w http.ResponseWriter, r *http.Request) {
	h := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(h)

	dataBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Error reading body of request for child restart", "msg", err.Error())
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}

	data := &comms.RestartConnectionChildRequest{}
	err = json.Unmarshal(dataBytes, data)
	if err != nil {
		logger.Error("Error unmarshaling body of request for a child restart", "msg", err.Error())
		w.WriteHeader(400)
		json.NewEncoder(w).Encode("Bad Request")
		return
	}

	logger.Info("Restarting child", "connection", data.ConnectionName, "child", data.ChildName)

	output, err := swanctlCombinedOutput("--initiate", "--child", data.ChildName, "--ike", data.ConnectionName, "--timeout", "3")
	if err != nil {
		logger.Error("Failed to restart child connection", "child", data.ChildName, "conn", data.ConnectionName, "err", err, "output", output)
		w.WriteHeader(500)
		json.NewEncoder(w).Encode("Action failed")
		return
	}
	logger.Info("Successfully initiated child connection", "child", data.ChildName, "conn", data.ConnectionName, "output", output)

	w.WriteHeader(200)
	json.NewEncoder(w).Encode("OK")
}

func checkStatus(conns, sas *swanparse.SwanAST) ([]FailureInfo, error) {
	v := NewStrongSwanVisitor(conns, sas)
	err := v.VisitAST(conns)
	if err != nil {
		return nil, err
	}

	failedConnections := make([]FailureInfo, 0)
	if len(v.MissingConns) > 0 || len(v.MissingChildren) > 0 {
		missingConns := slices.Collect(maps.Keys(v.MissingConns))
		// If a connection is fully failed, all of it's children are
		// naturally failed too. Let's filter them out
		maps.DeleteFunc(v.MissingChildren, func(k string, v []string) bool {
			return slices.Contains(missingConns, k)
		})
		for k, v := range v.MissingChildren {
			for _, child := range v {
				failedConnections = append(failedConnections, FailureInfo{
					Connection: k,
					Child:      child,
				})
			}
		}
		for _, conn := range missingConns {
			failedConnections = append(failedConnections, FailureInfo{
				Connection: conn,
				Child:      "",
			})
		}
	}

	return failedConnections, nil
}

func swanctlListConns() (*swanparse.SwanAST, error) {
	// Get the list of connections
	o, err := swanctl("--list-conns", "--raw")
	if err != nil {
		return nil, fmt.Errorf("Error listing swanctl connections: %w", err)
	}

	// TODO: github:TDT-AG/swanmon is a cli tool to output this info
	// in json format, we wouldn't need to do all that if we used it.
	// maybe contrib a way to make it a library instead of a cli tool
	connsAST, err := swanparse.Parse(o)
	if err != nil {
		return nil, fmt.Errorf("Error parsing swanctl connections: %w", err)
	}
	return connsAST, nil
}

func swanctlListSas() (*swanparse.SwanAST, error) {
	// Get the list of security associations
	o, err := swanctl("--list-sas", "--raw")
	if err != nil {

		return nil, fmt.Errorf("Error listing security associations: %w", err)
	}
	sasAST, err := swanparse.Parse(o)
	if err != nil {
		return nil, fmt.Errorf("Error parsing security associations: %w", err)
	}
	return sasAST, nil
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

// implements some logging interface prometheus wants
type PromLogger struct {
}

func (l PromLogger) Println(v ...any) {
	klog.V(5).Info(v...)
}

func waitForViciSock(socketsDir string) error {
	if !strings.HasSuffix(socketsDir, "/") {
		socketsDir = socketsDir + "/"
	}
	viciSocketPath := socketsDir + "charon.vici"

	ok, err := fileExists(viciSocketPath)
	if err != nil {
		return fmt.Errorf("Error while checking for socket existence (at path %s): %s", viciSocketPath, err)
	}

	if !ok {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("Error creating fsnotify watcher: %s", err)
		}
		err = fsw.Add(socketsDir)
		if err != nil {
			return fmt.Errorf("Couldn't add a path (%s) to watch: %s", viciSocketPath, err)
		}
		for {
			e := <-fsw.Events
			if e.Name == viciSocketPath && e.Has(fsnotify.Create) {
				break
			}
		}
	}
	return nil
}

func createK8sClient() (k8sclient client.Client, ctx context.Context, ctxCancel context.CancelFunc, err error) {
	ctx, ctxCancel = context.WithCancel(context.Background())
	config, err := rest.InClusterConfig()
	scheme := runtime.NewScheme()
	_ = ipmanv1.AddToScheme(scheme)

	if err != nil {
		err = fmt.Errorf("Error creating config for k8s client: %s", err)
		return
	}
	k8sclient, err = client.New(config, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		err = fmt.Errorf("Error creating k8s client: %s", err)
		return
	}
	return
}

func monitorConnections(ch chan<- FailureInfo, log *slog.Logger) {
	for {
		var vc *goviciclient.ViciClient
		var err error
		for {
			vc, err = goviciclient.NewViciClient(nil)
			if err != nil {
				log.Error("Failed to create a vici client", "error", err)
				time.Sleep(time.Second / 3)
				continue
			}
			break
		}

		connsAST, err := swanctlListConns()
		if err != nil {
			log.Error("error trying to initiate connection reconciliation", "msg", err)
			return
		}

		sasAST, err := swanctlListSas()
		if err != nil {
			log.Error("error trying to initiate connection reconciliation", "msg", err)
			return
		}

		failures, err := checkStatus(connsAST, sasAST)
		if err != nil {
			log.Error("Error reconciling connections", "err", err)
			return
		}
		for _, f := range failures {
			ch <- f
		}
		err = vc.Close()
		if err != nil {
			log.Error("Failed to close a vici client", "msg", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func downgradeConnectionStatus(ch <-chan FailureInfo, k8sclient client.Client, ctx context.Context, log *slog.Logger) {
	for {
		fi := <-ch
		ipsec := ipmanv1.IPSecConnection{}
		err := k8sclient.Get(ctx, types.NamespacedName{Name: fi.Connection}, &ipsec)
		if err != nil {
			log.Error("Error fetching IPSecConnection for status update", "msg", err)
			continue
		}
		if fi.Child == "" {
			ipsec.Status.State = "Down"
		} else {
			ipsec.Status.State = "Degraded"
			if ipsec.Status.ChildrenState == nil {
				ipsec.Status.ChildrenState = make(map[string]string, 1)
			}
			ipsec.Status.ChildrenState[fi.Child] = "Down"
		}
		err = k8sclient.Status().Update(ctx, &ipsec)
		if err != nil {
			log.Error("Error updating IPSecConnection status", "msg", err)
		}
	}
}

func main() {
	klog.InitFlags(nil)
	flag.Set("v", "5")
	flag.Parse()
	h := slog.NewJSONHandler(os.Stdout, nil)
	log := slog.New(h)

	k8sclient, ctx, ctxCancel, err := createK8sClient()
	if err != nil {
		panic(err)
	}

	err = waitForViciSock(os.Getenv("HOST_SOCKETS_PATH"))
	if err != nil {
		panic(err)
	}

	ch := make(chan FailureInfo, 10)
	go downgradeConnectionStatus(ch, k8sclient, ctx, log)
	go monitorConnections(ch, log)

	mux := http.NewServeMux()

	strongswanCollector := NewStrongswanCollector()
	strongswanCollector.init()
	pl := PromLogger{}
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		ErrorLog: pl,
	}))
	mux.HandleFunc("/p1ng", p0ng)
	mux.HandleFunc("/reload", reloadConfig)
	mux.HandleFunc("/configs", getConfigs)
	mux.HandleFunc("/conn-restart", RestartConnection)
	mux.HandleFunc("/child-restart", RestartConnectionChild)

	server := http.Server{
		Handler: mux,
	}

	listener, err := net.Listen("tcp", ":61410")
	if err != nil {
		log.Error("Error listening on port 61410", "msg", err)
		ctxCancel()
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-c
		ctxCancel()
		os.Exit(0)
	}()

	log.Info("Listening on socket", "port", 61410)
	if err := server.Serve(listener); err != nil {
		log.Error("Couldn't start server on listener", "msg", err)
		ctxCancel()
	}
}
