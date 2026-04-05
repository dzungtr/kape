// Package main is the entry point for the KAPE operator.
package main

import (
	"flag"
	"os"

	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffyaml"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

type config struct {
	MetricsAddr            string
	HealthProbeAddr        string
	LeaderElect            bool
	MaxConcurrentReconciles int
	KapeConfigNamespace    string
	KapeConfigName         string
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	log := ctrl.Log.WithName("main")

	fs := flag.NewFlagSet("kape-operator", flag.ExitOnError)
	cfg := &config{}

	fs.StringVar(&cfg.MetricsAddr, "metrics-bind-address", ":8080", "Address for the metrics endpoint")
	fs.StringVar(&cfg.HealthProbeAddr, "health-probe-bind-address", ":8081", "Address for the health probe endpoint")
	fs.BoolVar(&cfg.LeaderElect, "leader-elect", true, "Enable leader election")
	fs.IntVar(&cfg.MaxConcurrentReconciles, "max-concurrent-reconciles", 3, "Max parallel reconciles per controller")
	fs.StringVar(&cfg.KapeConfigNamespace, "kape-config-namespace", "kape-system", "Namespace of the kape-config ConfigMap")
	fs.StringVar(&cfg.KapeConfigName, "kape-config-name", "kape-config", "Name of the kape-config ConfigMap")

	if err := ff.Parse(fs, os.Args[1:],
		ff.WithEnvVarPrefix("KAPE_OPERATOR"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ffyaml.Parser),
		ff.WithAllowMissingConfigFile(true),
	); err != nil {
		log.Error(err, "parsing config")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: cfg.MetricsAddr},
		HealthProbeBindAddress: cfg.HealthProbeAddr,
		LeaderElection:         cfg.LeaderElect,
		LeaderElectionID:       "kape-operator-leader-election",
	})
	if err != nil {
		log.Error(err, "creating manager")
		os.Exit(1)
	}

	k8sClient := mgr.GetClient()

	// Adapters
	handlerRepo     := k8sadapters.NewHandlerRepository(k8sClient)
	configMapAdapt  := k8sadapters.NewConfigMapAdapter(k8sClient)
	saAdapt         := k8sadapters.NewServiceAccountAdapter(k8sClient)
	deployAdapt     := k8sadapters.NewDeploymentAdapter(k8sClient)
	cfgLoader       := k8sadapters.NewKapeConfigLoader(k8sClient, cfg.KapeConfigNamespace, cfg.KapeConfigName)
	renderer        := tomlrenderer.NewRenderer()

	// Domain reconciler
	handlerRec := reconcilehandler.New(
		handlerRepo,
		configMapAdapt,
		saAdapt,
		deployAdapt,
		renderer,
		cfgLoader,
	)

	// Register controller
	if err := kapecontroller.SetupHandlerReconciler(mgr, handlerRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeHandler controller")
		os.Exit(1)
	}

	// Health probes
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "setting up healthz check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "setting up readyz check")
		os.Exit(1)
	}

	log.Info("starting KAPE operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "running manager")
		os.Exit(1)
	}
}
