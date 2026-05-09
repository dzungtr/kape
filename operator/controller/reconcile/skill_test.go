package reconcile_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

func newSkillScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func validSkill(name, ns string) *v1alpha1.KapeSkill {
	return &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "A test skill",
			Instruction: "You are a test skill.",
		},
	}
}

func skillWithTool(name, ns, toolRef string) *v1alpha1.KapeSkill {
	s := validSkill(name, ns)
	s.Spec.Tools = []v1alpha1.SkillToolRef{{Ref: toolRef}}
	return s
}

func readyToolForSkill(name, ns string) *v1alpha1.KapeTool {
	return &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://mcp:8080"}}},
		Status: v1alpha1.KapeToolStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
			}},
		},
	}
}

func TestSkillReconciler_NoTools_SetsReady(t *testing.T) {
	skill := validSkill("my-skill", "kape-system")
	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "Ready", cond.Reason)
}

func TestSkillReconciler_ReadyTool_SetsReady(t *testing.T) {
	skill := skillWithTool("my-skill", "kape-system", "grafana-mcp")
	tool := readyToolForSkill("grafana-mcp", "kape-system")

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill, tool).
		WithStatusSubresource(skill, tool).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

func TestSkillReconciler_MissingTool_SetsNotReady(t *testing.T) {
	skill := skillWithTool("my-skill", "kape-system", "missing-tool")

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "KapeToolNotFound", cond.Reason)
}

func TestSkillReconciler_NotReadyTool_SetsNotReady(t *testing.T) {
	skill := skillWithTool("my-skill", "kape-system", "grafana-mcp")
	tool := readyToolForSkill("grafana-mcp", "kape-system")
	tool.Status.Conditions[0].Status = metav1.ConditionFalse // not ready

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill, tool).
		WithStatusSubresource(skill, tool).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "KapeToolNotReady", cond.Reason)
}

func TestSkillReconciler_InvalidSpec_SetsInvalidSpec(t *testing.T) {
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-skill", Namespace: "kape-system"},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "",     // missing
			Instruction: "test",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "bad-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "bad-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "InvalidSpec", cond.Reason)
}

func TestSkillReconciler_FinalizerAddedOnCreate(t *testing.T) {
	skill := validSkill("my-skill", "kape-system")
	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	assert.Contains(t, got.Finalizers, "kape.io/skill-protection")
}

func TestSkillReconciler_DeletionBlockedWhenHandlerReferences(t *testing.T) {
	now := metav1.Now()
	skill := validSkill("my-skill", "kape-system")
	skill.DeletionTimestamp = &now
	skill.Finalizers = []string{"kape.io/skill-protection"}

	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/skill-ref-my-skill": "true"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill, handler).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Contains(t, got.Finalizers, "kape.io/skill-protection")
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, "ReferencedByHandlers", cond.Reason)
}

func TestSkillReconciler_DeletionUnblockedWhenNoHandlerReferences(t *testing.T) {
	now := metav1.Now()
	skill := validSkill("my-skill", "kape-system")
	skill.DeletionTimestamp = &now
	skill.Finalizers = []string{"kape.io/skill-protection"}

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	if got != nil {
		assert.NotContains(t, got.Finalizers, "kape.io/skill-protection")
	}
}
