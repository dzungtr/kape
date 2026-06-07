// Package main is the envtest harness for the KAPE operator playground.
// It starts a local Kubernetes API server + etcd, installs all CRDs, runs
// the operator reconciler, writes kubeconfig.playground, and blocks until
// Ctrl-C. Requires KUBEBUILDER_ASSETS to point to apiserver+etcd binaries:
//
//	export KUBEBUILDER_ASSETS=$(setup-envtest use 1.32.0 --bin-dir /tmp/envtest-bins -p path)
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	qdrantadapter "github.com/kape-io/kape/operator/infra/qdrant"
	"github.com/kape-io/kape/operator/infra/ports"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	log := ctrl.Log.WithName("playground-operator")

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{"./crds"},
	}
	restCfg, err := env.Start()
	if err != nil {
		log.Error(err, "starting envtest")
		os.Exit(1)
	}
	defer env.Stop() //nolint:errcheck

	if err := writeKubeconfig(restCfg.Host, restCfg.CAData, restCfg.CertData, restCfg.KeyData); err != nil {
		log.Error(err, "writing kubeconfig.playground")
		os.Exit(1)
	}
	log.Info("kubeconfig.playground written — use: kubectl --kubeconfig kubeconfig.playground")

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	if err != nil {
		log.Error(err, "creating manager")
		os.Exit(1)
	}

	k8sClient := mgr.GetClient()
	platformCfg := domainconfig.KapeConfig{
		ClusterName:          "playground",
		HandlerImage:         "kape-runtime",
		HandlerImageVersion:  "dev",
		DefaultMaxIterations: 10,
	}

	cfgLoader := staticConfigLoader{cfg: platformCfg}

	// Build a discovery client for CRD detection.
	// In the playground the qdrant-operator is not installed, so
	// IsCRDInstalled returns false and memory tools stay MemoryReady=False/OperatorNotInstalled.
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		log.Error(err, "creating discovery client")
		os.Exit(1)
	}

	handlerRepo          := k8sadapters.NewHandlerRepository(k8sClient)
	schemaRepo           := k8sadapters.NewSchemaRepository(k8sClient)
	toolRepo             := k8sadapters.NewToolRepository(k8sClient)
	skillRepo            := k8sadapters.NewSkillRepository(k8sClient)
	configMapAdapt       := k8sadapters.NewConfigMapAdapter(k8sClient)
	kapeproxyConfigAdapt := k8sadapters.NewKapeproxyConfigAdapter(k8sClient)
	skillConfigMapAdapt  := k8sadapters.NewSkillConfigMapAdapter(k8sClient)
	saAdapt              := k8sadapters.NewServiceAccountAdapter(k8sClient)
	deployAdapt          := k8sadapters.NewDeploymentAdapter(k8sClient)
	scaledObjAdapt       := k8sadapters.NewScaledObjectAdapter(k8sClient)
	renderer             := tomlrenderer.NewRenderer()
	qdrantClusterAdapt   := qdrantadapter.NewQdrantClusterAdapter(k8sClient, discoveryClient)

	toolRecorder := mgr.GetEventRecorderFor("kapetool-controller")
	toolRec := reconcilehandler.NewToolReconciler(toolRepo, qdrantClusterAdapt, toolRecorder)
	if err := kapecontroller.SetupToolReconciler(mgr, toolRec, 3); err != nil {
		log.Error(err, "setting up KapeTool controller")
		os.Exit(1)
	}

	schemaRec := reconcilehandler.NewSchemaReconciler(schemaRepo, mgr.GetEventRecorderFor("kapeschema-controller"))
	if err := kapecontroller.SetupSchemaReconciler(mgr, schemaRec, 3); err != nil {
		log.Error(err, "setting up KapeSchema controller")
		os.Exit(1)
	}

	skillRec := reconcilehandler.NewSkillReconciler(skillRepo, toolRepo)
	if err := kapecontroller.SetupSkillReconciler(mgr, skillRec, 3); err != nil {
		log.Error(err, "setting up KapeSkill controller")
		os.Exit(1)
	}

	handlerRec := reconcilehandler.NewHandlerReconciler(
		handlerRepo, schemaRepo, toolRepo, skillRepo,
		configMapAdapt, kapeproxyConfigAdapt, skillConfigMapAdapt,
		saAdapt, deployAdapt, scaledObjAdapt,
		renderer, cfgLoader,
	)
	if err := kapecontroller.SetupHandlerReconciler(mgr, handlerRec, 3); err != nil {
		log.Error(err, "setting up KapeHandler controller")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Info("playground operator running — apply handlers with: kubectl --kubeconfig kubeconfig.playground apply -f examples/handlers/")
	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "manager exited with error")
		os.Exit(1)
	}
}

// staticConfigLoader satisfies ports.KapeConfigLoader with hardcoded playground values.
type staticConfigLoader struct {
	cfg domainconfig.KapeConfig
}

var _ ports.KapeConfigLoader = staticConfigLoader{}

func (s staticConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return s.cfg, nil
}

// Ensure unused imports compile.
var _ = fmt.Sprintf

func writeKubeconfig(host string, caData, certData, keyData []byte) error {
	cluster := clientcmdapi.NewCluster()
	cluster.Server = host
	cluster.CertificateAuthorityData = caData

	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientCertificateData = certData
	authInfo.ClientKeyData = keyData

	kubeCtx := clientcmdapi.NewContext()
	kubeCtx.Cluster = "playground"
	kubeCtx.AuthInfo = "playground-admin"

	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters["playground"] = cluster
	kubeconfig.AuthInfos["playground-admin"] = authInfo
	kubeconfig.Contexts["playground"] = kubeCtx
	kubeconfig.CurrentContext = "playground"

	out, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return err
	}
	return os.WriteFile("kubeconfig.playground", out, 0600)
}
