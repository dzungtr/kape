//go:build envtest

package integration_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

var platformCfg = domainconfig.KapeConfig{
	ClusterName:         "envtest",
	HandlerImage:        "kape-runtime",
	HandlerImageVersion: "dev",
}

var testScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(appsv1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}()

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Operator Integration Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{"../../../crds"},
		Scheme:            testScheme,
	}
	restCfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(restCfg).NotTo(BeNil())

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 testScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	Expect(err).NotTo(HaveOccurred())

	k8sClient = mgr.GetClient()
	cfgLoader := &fixedConfigLoader{cfg: platformCfg}

	schemaReconciler := reconcilehandler.NewSchemaReconciler(
		k8sadapters.NewSchemaRepository(k8sClient),
		mgr.GetEventRecorderFor("kapeschema"),
	)
	Expect(kapecontroller.SetupSchemaReconciler(mgr, schemaReconciler, 1)).To(Succeed())

	toolReconciler := reconcilehandler.NewToolReconciler(
		k8sadapters.NewToolRepository(k8sClient),
		k8sadapters.NewStatefulSetAdapter(k8sClient),
		cfgLoader,
	)
	Expect(kapecontroller.SetupToolReconciler(mgr, toolReconciler, 1)).To(Succeed())

	// noopScaledObjectAdapter is used because KEDA CRDs are not installed in envtest.
	handlerReconciler := reconcilehandler.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(k8sClient),
		k8sadapters.NewSchemaRepository(k8sClient),
		k8sadapters.NewToolRepository(k8sClient),
		k8sadapters.NewSkillRepository(k8sClient),
		k8sadapters.NewConfigMapAdapter(k8sClient),
		k8sadapters.NewKapeproxyConfigAdapter(k8sClient),
		k8sadapters.NewSkillConfigMapAdapter(k8sClient),
		k8sadapters.NewServiceAccountAdapter(k8sClient),
		k8sadapters.NewDeploymentAdapter(k8sClient),
		&noopScaledObjectAdapter{},
		tomlrenderer.NewRenderer(),
		cfgLoader,
	)
	Expect(kapecontroller.SetupHandlerReconciler(mgr, handlerReconciler, 1)).To(Succeed())

	go func() { _ = mgr.Start(ctx) }()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// fixedConfigLoader returns a fixed KapeConfig for integration tests.
type fixedConfigLoader struct {
	cfg domainconfig.KapeConfig
}

func (l *fixedConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return l.cfg, nil
}
