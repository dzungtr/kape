// Package main is the entry point for the KAPE operator.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffyaml"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	openshellwriter "github.com/kape-io/kape/operator/infra/openshell"
	"github.com/kape-io/kape/operator/infra/ports"
	qdrantadapter "github.com/kape-io/kape/operator/infra/qdrant"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
)

// newOpenShellWriter dials the gateway the operator writes provider records to
// (ADR-0088). No configured gateway yields a nil writer: the reconciler reports
// every credential as GatewayNotConfigured instead of guessing.
func newOpenShellWriter(cfg *config, log logr.Logger) (ports.OpenShellApplyPort, error) {
	if cfg.OpenShellGatewayAddr == "" {
		log.Info("OpenShell credential writer disabled: set KAPE_OPERATOR_OPENSHELL_GATEWAY_ADDR to enable it")
		return nil, nil
	}
	tlsCfg, err := loadMtlsConfig(cfg.OpenShellCredsDir)
	if err != nil {
		return nil, fmt.Errorf("loading OpenShell mTLS creds from %s: %w", cfg.OpenShellCredsDir, err)
	}
	conn, err := grpc.NewClient(cfg.OpenShellGatewayAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("dialling OpenShell gateway %s: %w", cfg.OpenShellGatewayAddr, err)
	}
	log.Info("OpenShell credential writer configured", "gateway", cfg.OpenShellGatewayAddr, "workspace", cfg.OpenShellWorkspace)
	return openshellwriter.NewWriter(conn, cfg.OpenShellWorkspace), nil
}

// loadMtlsConfig builds the mTLS config the gateway requires from the creds dir
// layout (ca.crt/tls.crt/tls.key).
func loadMtlsConfig(dir string) (*tls.Config, error) {
	if dir == "" {
		return nil, fmt.Errorf("creds dir is empty")
	}
	caPEM, err := os.ReadFile(dir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read ca.crt: %w", err)
	}
	certPEM, err := os.ReadFile(dir + "/tls.crt")
	if err != nil {
		return nil, fmt.Errorf("read tls.crt: %w", err)
	}
	keyPEM, err := os.ReadFile(dir + "/tls.key")
	if err != nil {
		return nil, fmt.Errorf("read tls.key: %w", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse client key pair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse ca.crt")
	}
	return &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// controllerWiring assembles the operator-level cluster wiring: the settings
// every rendered kape-controller Deployment receives from the operator's own
// configuration rather than from the KapeAgentServer CR.
func controllerWiring(cfg *config) (domainconfig.ControllerWiring, error) {
	wiring := domainconfig.ControllerWiring{
		GatewayAddr:     cfg.ControllerGatewayAddr,
		CredsDir:        cfg.ControllerCredsDir,
		CredsSecret:     cfg.ControllerCredsSecret,
		DefaultProvider: cfg.ControllerDefaultProvider,
		Transport:       cfg.ControllerTransport,
		EventStore:      cfg.ControllerEventStore,
	}
	if cfg.ControllerExtraEnv != "" {
		if err := json.Unmarshal([]byte(cfg.ControllerExtraEnv), &wiring.ExtraEnv); err != nil {
			return domainconfig.ControllerWiring{}, fmt.Errorf("controller-extra-env: %w", err)
		}
	}
	if cfg.ControllerExtraEnvFrom != "" {
		if err := json.Unmarshal([]byte(cfg.ControllerExtraEnvFrom), &wiring.ExtraEnvFrom); err != nil {
			return domainconfig.ControllerWiring{}, fmt.Errorf("controller-extra-env-from: %w", err)
		}
	}
	return wiring, nil
}

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

type config struct {
	MetricsAddr             string
	HealthProbeAddr         string
	LeaderElect             bool
	MaxConcurrentReconciles int
	KapeConfigNamespace     string
	KapeConfigName          string
	// Controller cluster wiring (spec #261 D5): the operator-level env written
	// into every CR-rendered kape-controller Deployment. Unset values are never
	// defaulted — the KapeAgentServer Ready condition reports them.
	ControllerGatewayAddr     string
	ControllerCredsDir        string
	ControllerCredsSecret     string
	ControllerDefaultProvider string
	ControllerTransport       string
	ControllerEventStore      string
	// ControllerExtraEnv / ControllerExtraEnvFrom are JSON-encoded passthrough
	// lists ([]corev1.EnvVar / []corev1.EnvFromSource). The chart translates
	// optional feature wiring (monitor, revive, …) into them; the operator
	// names no feature itself.
	ControllerExtraEnv     string
	ControllerExtraEnvFrom string

	// OpenShell gateway the operator itself writes provider records to
	// (ADR-0088, spec #261 M3). Empty gateway address disables the writer: every
	// credential then reports Pushed=False/GatewayNotConfigured rather than
	// silently staying unprovisioned.
	OpenShellGatewayAddr string
	OpenShellCredsDir    string
	OpenShellWorkspace   string
}

// operatorEnvVarPrefix is the env-var namespace ff derives flag bindings from:
// KAPE_OPERATOR_<FLAG_NAME with '-' replaced by '_', upper-cased>.
const operatorEnvVarPrefix = "KAPE_OPERATOR"

// newFlagSet registers the operator's flags. main and the chart/env binding
// test share it so the env names the chart must set are derived from the flag
// definitions rather than spelled out a second time.
func newFlagSet(cfg *config) *flag.FlagSet {
	fs := flag.NewFlagSet("kape-operator", flag.ExitOnError)

	fs.StringVar(&cfg.MetricsAddr, "metrics-bind-address", ":8080", "Address for the metrics endpoint")
	fs.StringVar(&cfg.HealthProbeAddr, "health-probe-bind-address", ":8081", "Address for the health probe endpoint")
	fs.BoolVar(&cfg.LeaderElect, "leader-elect", true, "Enable leader election")
	fs.IntVar(&cfg.MaxConcurrentReconciles, "max-concurrent-reconciles", 3, "Max parallel reconciles per controller")
	fs.StringVar(&cfg.KapeConfigNamespace, "kape-config-namespace", "kape-system", "Namespace of the kape-config ConfigMap")
	fs.StringVar(&cfg.KapeConfigName, "kape-config-name", "kape-config", "Name of the kape-config ConfigMap")
	fs.StringVar(&cfg.ControllerGatewayAddr, "controller-gateway-addr", "", "in-cluster OpenShell gateway host:port written as KAPE_GATEWAY_ADDR into rendered controllers")
	fs.StringVar(&cfg.ControllerCredsDir, "controller-creds-dir", "", "mTLS credentials mount path written as KAPE_CREDS_DIR into rendered controllers")
	fs.StringVar(&cfg.ControllerDefaultProvider, "controller-default-provider", "", "sandbox provider written as KAPE_DEFAULT_PROVIDER into rendered controllers")
	fs.StringVar(&cfg.ControllerCredsSecret, "controller-creds-secret", "", "Secret mounted read-only at the creds dir of rendered controllers")
	fs.StringVar(&cfg.ControllerTransport, "controller-transport", "", "data-plane transport written as KAPE_TRANSPORT into rendered controllers (pull|exec)")
	fs.StringVar(&cfg.ControllerEventStore, "controller-event-store", "", "durable event log path written as KAPE_EVENT_STORE into rendered controllers")
	fs.StringVar(&cfg.ControllerExtraEnv, "controller-extra-env", "", "JSON []corev1.EnvVar copied into rendered controllers (contract keys are ignored)")
	fs.StringVar(&cfg.ControllerExtraEnvFrom, "controller-extra-env-from", "", "JSON []corev1.EnvFromSource copied into rendered controllers")
	fs.StringVar(&cfg.OpenShellGatewayAddr, "openshell-gateway-addr", "", "OpenShell gateway host:port the operator writes provider records to (empty disables credential push)")
	fs.StringVar(&cfg.OpenShellCredsDir, "openshell-creds-dir", "", "mTLS creds dir (ca.crt/tls.crt/tls.key) for the operator's own gateway connection")
	fs.StringVar(&cfg.OpenShellWorkspace, "openshell-workspace", "default", "OpenShell workspace provider records are created in")

	return fs
}

// parseConfig binds flags from the command line, KAPE_OPERATOR_* env vars and
// an optional --config file, in that priority order.
func parseConfig(fs *flag.FlagSet, args []string) error {
	return ff.Parse(fs, args,
		ff.WithEnvVarPrefix(operatorEnvVarPrefix),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ffyaml.Parser),
		ff.WithAllowMissingConfigFile(true),
	)
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	log := ctrl.Log.WithName("main")

	cfg := &config{}

	if err := parseConfig(newFlagSet(cfg), os.Args[1:]); err != nil {
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

	// Build a discovery client from the manager's REST config for CRD discovery.
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		log.Error(err, "creating discovery client")
		os.Exit(1)
	}

	// Adapters
	handlerRepo := k8sadapters.NewHandlerRepository(k8sClient)
	schemaRepo := k8sadapters.NewSchemaRepository(k8sClient)
	toolRepo := k8sadapters.NewToolRepository(k8sClient)
	skillRepo := k8sadapters.NewSkillRepository(k8sClient)
	configMapAdapt := k8sadapters.NewConfigMapAdapter(k8sClient)
	kapeproxyConfigAdapt := k8sadapters.NewKapeproxyConfigAdapter(k8sClient)
	saAdapt := k8sadapters.NewServiceAccountAdapter(k8sClient)
	deployAdapt := k8sadapters.NewDeploymentAdapter(k8sClient)
	scaledObjAdapt := k8sadapters.NewScaledObjectAdapter(k8sClient)
	cfgLoader := k8sadapters.NewKapeConfigLoader(k8sClient, cfg.KapeConfigNamespace, cfg.KapeConfigName)
	renderer := tomlrenderer.NewRenderer()

	// QdrantCluster adapter — delegates provisioning to the upstream qdrant-operator CRD.
	qdrantClusterAdapt := qdrantadapter.NewQdrantClusterAdapter(k8sClient, discoveryClient)

	podReader := k8sadapters.NewPodReaderAdapter(k8sClient)

	// KapeToolReconciler
	toolRecorder := mgr.GetEventRecorderFor("kapetool-controller")
	toolRec := reconcilehandler.NewToolReconciler(toolRepo, qdrantClusterAdapt, toolRecorder)
	if err := kapecontroller.SetupToolReconciler(mgr, toolRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeTool controller")
		os.Exit(1)
	}

	// KapeSchemaReconciler
	schemaRecorder := mgr.GetEventRecorderFor("kapeschema-controller")
	schemaRec := reconcilehandler.NewSchemaReconciler(schemaRepo, schemaRecorder)
	if err := kapecontroller.SetupSchemaReconciler(mgr, schemaRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeSchema controller")
		os.Exit(1)
	}

	// KapeSkillReconciler
	skillRec := reconcilehandler.NewSkillReconciler(skillRepo, toolRepo)
	if err := kapecontroller.SetupSkillReconciler(mgr, skillRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeSkill controller")
		os.Exit(1)
	}

	skillConfigMapAdapt := k8sadapters.NewSkillConfigMapAdapter(k8sClient)

	// KapeHandlerReconciler
	handlerRec := reconcilehandler.NewHandlerReconciler(
		handlerRepo,
		schemaRepo,
		toolRepo,
		skillRepo,
		configMapAdapt,
		kapeproxyConfigAdapt,
		skillConfigMapAdapt,
		saAdapt,
		deployAdapt,
		scaledObjAdapt,
		renderer,
		cfgLoader,
	)

	// Register controller
	if err := kapecontroller.SetupHandlerReconciler(mgr, handlerRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeHandler controller")
		os.Exit(1)
	}

	// KapeProxyReconciler
	proxyRec := reconcilehandler.NewKapeProxyReconciler(podReader, handlerRepo)
	if err := kapecontroller.SetupKapeProxyReconciler(mgr, proxyRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeProxy controller")
		os.Exit(1)
	}

	// KapeAgentServerReconciler
	wiring, err := controllerWiring(cfg)
	if err != nil {
		log.Error(err, "parsing controller wiring")
		os.Exit(1)
	}
	credentialWriter, err := newOpenShellWriter(cfg, log)
	if err != nil {
		log.Error(err, "configuring OpenShell credential writer")
		os.Exit(1)
	}
	agentServerRec := reconcilehandler.NewAgentServerReconciler(
		k8sadapters.NewAgentServerRepository(k8sClient),
		k8sadapters.NewAgentServerAdapter(k8sClient),
		k8sadapters.NewSecretResolver(k8sClient),
		credentialWriter,
		wiring,
	)
	if err := kapecontroller.SetupAgentServerReconciler(mgr, agentServerRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeAgentServer controller")
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
