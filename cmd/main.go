package main

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ipmanv1 "dialo.ai/ipman/api/v1"
	ipmanwhv1 "dialo.ai/ipman/internal/webhook/v1"
	cont "dialo.ai/ipman/internal/controller"
)

var (
	logger = logf.Log.WithName("Setup")
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ipmanv1.AddToScheme(scheme))
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

	logger.Info("Creating controller")
	if err = (&cont.IpmanReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "Creating controller failed")
		os.Exit(1)
	}

	logger.Info("New webhook")
	whServer := webhook.NewServer(webhook.Options{
		Port: 8443,
		CertDir: "/etc/webhook/certs",
	})

	if err = mgr.Add(whServer); err != nil{
		logger.Error(err, "Error registering wh server with the manager")
		os.Exit(1)
	}
	

	whh := ipmanwhv1.WebhookHandler{
		Client: mgr.GetClient(),
	}
	whServer.Register("/mutating", &whh)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "problem running manager")
		os.Exit(1)
	}
}
