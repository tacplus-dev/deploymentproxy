// Command deploymentproxy runs the TenantNamespace controller. It has no network listener.
package main

import (
	"flag"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	deploymentproxyv1 "github.com/tacplus-dev/deploymentproxy/api/v1"
	"github.com/tacplus-dev/deploymentproxy/internal/config"
	"github.com/tacplus-dev/deploymentproxy/internal/controller"
)

func main() {
	configPath := flag.String("config", "/etc/deploymentproxy/config.yaml", "the config file (the deploymentproxy ConfigMap's config.yaml)")
	resync := flag.Duration("resync", 10*time.Minute, "how often every TenantNamespace is reconciled again")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("deploymentproxy")

	// Fail at start on a bad config; afterwards a bad edit is reported on each TenantNamespace.
	if _, err := config.Load(*configPath); err != nil {
		log.Error(err, "loading config")
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}
	if err := deploymentproxyv1.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}

	cacheOpts := controller.CacheOptions()
	cacheOpts.SyncPeriod = resync
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cacheOpts,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		log.Error(err, "creating manager")
		os.Exit(1)
	}
	r := &controller.Reconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Config:    func() (*config.Config, error) { return config.Load(*configPath) },
	}
	if err := r.SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up controller")
		os.Exit(1)
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "running")
		os.Exit(1)
	}
}
