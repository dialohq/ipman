package main

import (
	"os"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ipmanv1 "dialo.ai/ipman/api/v1"
	"dialo.ai/ipman/internal/controller"
	cont "dialo.ai/ipman/internal/controller"
	ipmanwhv1 "dialo.ai/ipman/internal/webhook/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

var (
	logger = logf.Log.WithName("Setup")
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ipmanv1.AddToScheme(scheme))
	utilruntime.Must(promv1.AddToScheme(scheme))
}

func main() {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
	})
	if err != nil {
		logger.Error(err, "Creating manager failed")
		os.Exit(1)
	}
	podTimeout, err := strconv.ParseInt(os.Getenv("POD_WAIT_TIMEOUT"), 10, 64)
	if err != nil {
		logger.Error(err, "Couldn't parse wait for pod timeout")
		os.Exit(1)
	}

	// TODO: validate everything is not nil else error

	e := controller.Envs{
		NamespaceName:            os.Getenv("NAMESPACE_NAME"),
		XfrminionImage:           os.Getenv("XFRMINION_IMAGE"),
		VxlandlordImage:          os.Getenv("VXLANDLORD_IMAGE"),
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
		logger.Info("Enabling monitoring", "release", e.MonitoringReleaseName)
	}

	logger.Info("Creating controller")
	if err = (&cont.IPSecConnectionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Env:    e,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "Creating controller failed")
		os.Exit(1)
	}

	logger.Info("New webhook")
	whServer := webhook.NewServer(webhook.Options{
		Port:    ipmanv1.WebhookServerPort,
		CertDir: ipmanv1.WebhookServerCertDir,
	})

	if err = mgr.Add(whServer); err != nil {
		logger.Error(err, "Error registering wh server with the manager")
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

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "problem running manager")
		os.Exit(1)
	}
}
