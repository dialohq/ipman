package main

import (
	"net/http"
	"os"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/internal/controller"
	cont "dialo.ai/ipman/internal/controller"
	ipmanwhv1 "dialo.ai/ipman/internal/webhook/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

var (
	log    = logf.Log.WithName("Setup")
	scheme = runtime.NewScheme()
)

var (
	connectionsStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "strongswan",
		Name:      "connections_status",
		Help:      "Returns all existing connections, value of 0 means the connection is not loaded into the charon daemon, value of 1 means it is",
	}, []string{"connection"})
	failedConnectionLoadCnt = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "strongswan",
		Name:      "failed_connection_load_cnt",
		Help:      "The total number of times a connection was not loaded",
	})
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ipmanv1.AddToScheme(scheme))
	utilruntime.Must(promv1.AddToScheme(scheme))
	prometheus.MustRegister(connectionsStatus)
	prometheus.MustRegister(failedConnectionLoadCnt)
}

func main() {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: ":8080",
			CertDir:     ipmanv1.WebhookServerCertDir,
		},
	})
	if err != nil {
		log.Error(err, "Creating manager failed")
		os.Exit(1)
	}
	podTimeout, err := strconv.ParseInt(os.Getenv("POD_WAIT_TIMEOUT"), 10, 64)
	if err != nil {
		log.Error(err, "Couldn't parse wait for pod timeout")
		os.Exit(1)
	}
	ttlEnv := os.Getenv("XFRMINJECTOR_TTL_SECONDS")
	var ttl *int32 = nil
	if ttlEnv != "" {
		inter, err := strconv.ParseInt(ttlEnv, 10, 32)
		if err != nil {
			log.Error(err, "couldn't parse ttl seconds")
			os.Exit(1)
		}
		inter2 := int32(inter)
		ttl = &inter2
	}

	// TODO: validate everything is not nil else error
	e := controller.Envs{
		NamespaceName:            os.Getenv("NAMESPACE_NAME"),
		XfrminionImage:           os.Getenv("XFRMINION_IMAGE"),
		VxlandlordImage:          os.Getenv("VXLANDLORD_IMAGE"),
		XfrminjectorImage:        os.Getenv("XFRMINJECTOR_IMAGE"),
		XfrminjectorPullPolicy:   os.Getenv("XFRMINJECTOR_PULL_POLICY"),
		XfrminjectorTTL:          ttl,
		RestctlImage:             os.Getenv("RESTCTL_IMAGE"),
		RestctlPullPolicy:        os.Getenv("RESTCTL_PULL_POLICY"),
		CaddyImage:               os.Getenv("CADDY_IMAGE"),
		CharonDaemonImage:        os.Getenv("CHARONDAEMON_IMAGE"),
		HostSocketsPath:          os.Getenv("HOST_SOCKETS_PATH"),
		XfrminionPullPolicy:      os.Getenv("XFRMINION_PULL_POLICY"),
		CharonDaemonPullPolicy:   os.Getenv("CHARON_PULL_POLICY"),
		CaddyProxyPullPolicy:     os.Getenv("PROXY_PULL_POLICY"),
		IsMonitoringEnabled:      os.Getenv("MONITORING_ENABLED") == "true",
		MonitoringScrapeInterval: os.Getenv("MONITORING_SCRAPE_INTERVAL"),
		MonitoringReleaseName:    os.Getenv("MONITORING_RELEASE_NAME"),
		WaitForPodTimeoutSeconds: podTimeout,
		IsTest:                   false,
	}
	if e.IsMonitoringEnabled {
		log.Info("Enabling monitoring", "release", e.MonitoringReleaseName)
	}

	log.Info("Creating controller")
	rec := &cont.IPSecConnectionReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Env:                     e,
		NotLoadedConns:          []string{},
		AllConns:                []string{},
		ConnectionsStatusMetric: connectionsStatus,
		NotLoadedMetric:         failedConnectionLoadCnt,
	}

	if err = rec.SetupWithManager(mgr); err != nil {
		log.Error(err, "Creating controller failed")
		os.Exit(1)
	}

	log.Info("New webhook")
	whServer := webhook.NewServer(webhook.Options{
		Port:    ipmanv1.WebhookServerPort,
		CertDir: ipmanv1.WebhookServerCertDir,
	})

	if err = mgr.Add(whServer); err != nil {
		log.Error(err, "Error registering wh server with the manager")
		os.Exit(1)
	}

	mwh := ipmanwhv1.MutatingWebhookHandler{
		Client: mgr.GetClient(),
		Config: *mgr.GetConfig(),
		Env:    e,
	}
	vwh := ipmanwhv1.ValidatingWebhookHandler{
		Client: mgr.GetClient(),
		Config: *mgr.GetConfig(),
		Env:    e,
	}
	whServer.Register("/mutating", &mwh)
	whServer.Register("/validating", &vwh)

	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":61410", nil)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}
