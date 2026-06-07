//go:build envtest

package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

var enqueueScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(appsv1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}()

// watchesNoopScaledObjectAdapter stubs out KEDA for the watches envtest.
// KEDA CRDs are not installed in the envtest cluster, so ScaledObject calls
// would fail with a "no kind registered" error.
type watchesNoopScaledObjectAdapter struct{}

func (n *watchesNoopScaledObjectAdapter) Ensure(_ context.Context, _ *v1alpha1.KapeHandler, _ string, _ domainconfig.KapeConfig) error {
	return nil
}
func (n *watchesNoopScaledObjectAdapter) GetConsumerName(_ context.Context, _ types.NamespacedName) (string, bool, error) {
	return "", false, nil
}
func (n *watchesNoopScaledObjectAdapter) Delete(_ context.Context, _ types.NamespacedName) error {
	return nil
}

// TestSkillWatch_SpecChange_TriggersHandlerReconcile verifies that editing a KapeSkill's spec
// triggers re-reconciliation of every KapeHandler that references it (via kape.io/skill-ref-* label),
// observable as a rollout-hash annotation change on the handler's Deployment.
func TestSkillWatch_SpecChange_TriggersHandlerReconcile(t *testing.T) {
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../crds"},
		Scheme:            enqueueScheme,
	}
	restCfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 enqueueScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	require.NoError(t, err)

	k8sClient := mgr.GetClient()
	platformCfg := domainconfig.KapeConfig{
		ClusterName:         "envtest",
		HandlerImage:        "kape-runtime",
		HandlerImageVersion: "dev",
	}
	cfgLoader := &envtestConfigLoader{cfg: platformCfg}

	innerReconciler := reconcilehandler.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(k8sClient),
		k8sadapters.NewSchemaRepository(k8sClient),
		k8sadapters.NewToolRepository(k8sClient),
		k8sadapters.NewSkillRepository(k8sClient),
		k8sadapters.NewConfigMapAdapter(k8sClient),
		k8sadapters.NewKapeproxyConfigAdapter(k8sClient),
		k8sadapters.NewSkillConfigMapAdapter(k8sClient),
		k8sadapters.NewServiceAccountAdapter(k8sClient),
		k8sadapters.NewDeploymentAdapter(k8sClient),
		&watchesNoopScaledObjectAdapter{},
		tomlrenderer.NewRenderer(),
		cfgLoader,
	)
	err = kapecontroller.SetupHandlerReconciler(mgr, innerReconciler, 1)
	require.NoError(t, err)

	go func() { _ = mgr.Start(ctx) }()

	ns := "default"

	// 1. Apply a Ready KapeSchema.
	addlProps := false
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "test-schema", Namespace: ns},
		Spec: v1alpha1.KapeSchemaSpec{
			Version: "v1",
			JSONSchema: v1alpha1.JSONSchemaObject{
				Type:     "object",
				Required: []string{"msg"},
				Properties: map[string]apiextensionsv1.JSON{
					"msg": {Raw: []byte(`{"type":"string"}`)},
				},
				AdditionalProperties: &addlProps,
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, schema))
	schema.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, k8sClient.Status().Update(ctx, schema))

	// 2. Apply a KapeSkill and mark it Ready so the handler dependency gate passes.
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: ns},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "Analyst skill",
			Instruction: "You are an analyst.",
		},
	}
	require.NoError(t, k8sClient.Create(ctx, skill))
	skill.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Ready",
		LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, k8sClient.Status().Update(ctx, skill))

	// 3. Apply a KapeHandler that references the skill in Spec.Skills.
	// The reconciler will write kape.io/skill-ref-analyst=true after the first reconcile.
	h := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: ns,
		},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: "test-schema",
			Skills:    []v1alpha1.SkillRef{{Ref: "analyst"}},
			Tools:     []v1alpha1.ToolRef{},
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, h))

	// 4. Wait for initial reconcile to create the Deployment with rollout-hash annotation.
	depKey := types.NamespacedName{Name: "kape-handler-my-handler", Namespace: ns}
	var initialDep appsv1.Deployment
	require.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, depKey, &initialDep)
		return err == nil && initialDep.Annotations["kape.io/rollout-hash"] != ""
	}, 10*time.Second, 200*time.Millisecond, "deployment should be created with rollout-hash annotation")

	initialHash := initialDep.Annotations["kape.io/rollout-hash"]

	// 5. Patch the KapeSkill spec — this triggers MapSkillToHandlers via the secondary watch.
	// The handler must now have kape.io/skill-ref-analyst=true label (set by step 4's reconcile).
	patch := client.MergeFrom(skill.DeepCopy())
	skill.Spec.Instruction = "You are an expert analyst with domain expertise."
	require.NoError(t, k8sClient.Patch(ctx, skill, patch))

	// 6. Wait for the Deployment rollout-hash to change, proving the handler was re-reconciled.
	assert.Eventually(t, func() bool {
		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, depKey, &dep); err != nil {
			return false
		}
		newHash := dep.Annotations["kape.io/rollout-hash"]
		return newHash != "" && newHash != initialHash
	}, 10*time.Second, 200*time.Millisecond, "rollout-hash should change after KapeSkill spec update")
}

// envtestConfigLoader returns a fixed KapeConfig for integration tests.
type envtestConfigLoader struct {
	cfg domainconfig.KapeConfig
}

func (l *envtestConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return l.cfg, nil
}
