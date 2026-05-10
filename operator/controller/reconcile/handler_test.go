package reconcile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

func newHandlerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newReconciler(c client.Client) *reconcile.HandlerReconciler {
	return reconcile.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(c),
		k8sadapters.NewSchemaRepository(c),
		k8sadapters.NewToolRepository(c),
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewConfigMapAdapter(c),
		k8sadapters.NewSkillConfigMapAdapter(c),
		k8sadapters.NewServiceAccountAdapter(c),
		k8sadapters.NewDeploymentAdapter(c),
		k8sadapters.NewScaledObjectAdapter(c),
		tomlrenderer.NewRenderer(),
		&fakeConfigLoader{},
	)
}

func readySchema(name, ns string) *v1alpha1.KapeSchema {
	return &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeSchemaSpec{
			Version: "v1",
			JSONSchema: v1alpha1.JSONSchemaObject{
				Type:     "object",
				Required: []string{"decision"},
				Properties: map[string]apiextensionsv1.JSON{
					"decision": {Raw: []byte(`{"type":"string"}`)},
				},
			},
		},
		Status: v1alpha1.KapeSchemaStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Valid",
			}},
		},
	}
}

func readyTool(name, ns, toolType string) *v1alpha1.KapeTool {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.KapeToolSpec{Type: toolType},
		Status: v1alpha1.KapeToolStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
			}},
		},
	}
	if toolType == "mcp" {
		tool.Spec.MCP = &v1alpha1.MCPSpec{
			Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://mcp:8080"},
		}
	}
	if toolType == "memory" {
		tool.Spec.Memory = &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"}
		tool.Status.QdrantEndpoint = "http://kape-memory-" + name + ".kape-system:6333"
	}
	return tool
}

func readySkill(name, ns string, lazy bool, toolRefs []string) *v1alpha1.KapeSkill {
	refs := make([]v1alpha1.SkillToolRef, len(toolRefs))
	for i, r := range toolRefs {
		refs[i] = v1alpha1.SkillToolRef{Ref: r}
	}
	return &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "desc " + name,
			Instruction: "INSTR-" + strings.ToUpper(name),
			LazyLoad:    lazy,
			Tools:       refs,
		},
		Status: v1alpha1.KapeSkillStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
		},
	}
}

func baseKapeHandler(name, ns, schemaRef string, toolRefs []string, skillRefs ...string) *v1alpha1.KapeHandler {
	tools := make([]v1alpha1.ToolRef, len(toolRefs))
	for i, r := range toolRefs {
		tools[i] = v1alpha1.ToolRef{Ref: r}
	}
	skills := make([]v1alpha1.SkillRef, len(skillRefs))
	for i, r := range skillRefs {
		skills[i] = v1alpha1.SkillRef{Ref: r}
	}
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: schemaRef,
			Tools:     tools,
			Skills:    skills,
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
}

func TestHandlerReconciler_SchemaNotReady_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	schema.Status.Conditions[0].Status = metav1.ConditionFalse // not ready
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", nil)

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema).
		WithStatusSubresource(handler, schema).
		Build()

	r := newReconciler(c)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})
	depsCond := findCondition(got.Status.Conditions, "DependenciesReady")
	require.NotNil(t, depsCond)
	assert.Equal(t, metav1.ConditionFalse, depsCond.Status)
}

func TestHandlerReconciler_ToolNotReady_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("grafana-mcp", "kape-system", "mcp")
	tool.Status.Conditions[0].Status = metav1.ConditionFalse // not ready
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", []string{"grafana-mcp"})

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool).
		WithStatusSubresource(handler, schema, tool).
		Build()

	r := newReconciler(c)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))
}

func TestHandlerReconciler_InvalidScaling_TerminalNoRequeue(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", nil)
	handler.Spec.Scaling = &v1alpha1.ScalingSpec{ScaleToZero: true, MinReplicas: 1} // invalid

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema).
		WithStatusSubresource(handler, schema).
		Build()

	r := newReconciler(c)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, false, result.Requeue)
	assert.Equal(t, int64(0), int64(result.RequeueAfter))
}

func TestHandlerReconciler_AllDepsReady_CreatesResources(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("grafana-mcp", "kape-system", "mcp")
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", []string{"grafana-mcp"})

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool).
		WithStatusSubresource(handler, schema, tool).
		Build()

	r := newReconciler(c)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(60), int64(result.RequeueAfter.Seconds()))

	// ConfigMap created
	var cm corev1.ConfigMap
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"}, &cm)
	require.NoError(t, err)

	// Deployment created
	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)
	assert.Len(t, dep.Spec.Template.Spec.Containers, 2) // handler + sidecar

	// ServiceAccount created
	var sa corev1.ServiceAccount
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"}, &sa)
	require.NoError(t, err)
}

// --- Slice 3 new tests ---

func TestHandlerReconciler_SkillNotFound_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "missing-skill")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema).
		WithStatusSubresource(handler, schema).
		Build()
	r := newReconciler(c)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	dep := findCondition(got.Status.Conditions, "DependenciesReady")
	require.NotNil(t, dep)
	assert.Equal(t, metav1.ConditionFalse, dep.Status)
	assert.Equal(t, "KapeSkillNotFound", dep.Reason)
}

func TestHandlerReconciler_SkillNotReady_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	skill := readySkill("not-ready-skill", "kape-system", false, nil)
	skill.Status.Conditions[0].Status = metav1.ConditionFalse
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "not-ready-skill")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, skill).
		WithStatusSubresource(handler, schema, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	dep := findCondition(got.Status.Conditions, "DependenciesReady")
	require.NotNil(t, dep)
	assert.Equal(t, metav1.ConditionFalse, dep.Status)
	assert.Equal(t, "KapeSkillNotReady", dep.Reason)
}

func TestHandlerReconciler_LazySkill_CreatesKapeSkillsConfigMapAndMount(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", true, []string{"order-mcp"})
	skill.Spec.Description = "investigates orders"
	skill.Spec.Instruction = "do-the-investigation"
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	// kape-skills-h ConfigMap exists with one file
	var cm corev1.ConfigMap
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm)
	require.NoError(t, err)
	assert.Equal(t, "do-the-investigation", cm.Data["check-orders.txt"])

	// Deployment mounts /etc/kape/skills
	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)
	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
	var foundSkillsMount bool
	for _, m := range mounts {
		if m.Name == "kape-skills" && m.MountPath == "/etc/kape/skills" {
			foundSkillsMount = true
		}
	}
	assert.True(t, foundSkillsMount, "handler container must mount /etc/kape/skills when lazy skills present; mounts=%+v", mounts)
}

func TestHandlerReconciler_OnlyEagerSkills_NoKapeSkillsConfigMap(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("inline-skill", "kape-system", false, []string{"order-mcp"})
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "inline-skill")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	// No kape-skills-h ConfigMap
	var cm corev1.ConfigMap
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm)
	assert.True(t, apierrors.IsNotFound(err), "kape-skills-h must NOT exist when only eager skills present, got err=%v", err)

	// Deployment does NOT mount /etc/kape/skills
	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)
	for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		assert.NotEqual(t, "kape-skills", m.Name, "no kape-skills mount when only eager skills present")
	}
}

func TestHandlerReconciler_SkillRemoved_DeletesKapeSkillsConfigMap(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", true, []string{"order-mcp"})
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	// First reconcile: ConfigMap created
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var cm corev1.ConfigMap
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm))

	// Mutate handler to remove the skill ref
	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	got.Spec.Skills = nil
	require.NoError(t, c.Update(context.Background(), got))

	// Second reconcile: ConfigMap deleted
	_, err = r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm)
	assert.True(t, apierrors.IsNotFound(err), "kape-skills-h must be deleted after skill ref removed; got err=%v", err)
}

func TestHandlerReconciler_LabelSync_TransitiveToolAndSkillLabels(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	directTool := readyTool("k8s-mcp", "kape-system", "mcp")
	skillTool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", false, []string{"order-mcp"})
	handler := baseKapeHandler("h", "kape-system", "my-schema", []string{"k8s-mcp"}, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, directTool, skillTool, skill).
		WithStatusSubresource(handler, schema, directTool, skillTool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Equal(t, "true", got.Labels["kape.io/tool-ref-k8s-mcp"], "direct tool label")
	assert.Equal(t, "true", got.Labels["kape.io/tool-ref-order-mcp"], "transitive (skill-pulled) tool label per D8")
	assert.Equal(t, "true", got.Labels["kape.io/skill-ref-check-orders"], "skill ref label per D7")
}

func TestComputeRolloutHash_ChangesWhenSkillSpecChanges(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", false, []string{"order-mcp"})
	skill.Spec.Instruction = "VERSION-1"
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var dep1 appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep1))
	hash1 := dep1.Annotations["kape.io/rollout-hash"]
	require.NotEmpty(t, hash1)

	// Mutate skill instruction
	gotSkill := &v1alpha1.KapeSkill{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "check-orders", Namespace: "kape-system"}, gotSkill))
	gotSkill.Spec.Instruction = "VERSION-2"
	require.NoError(t, c.Update(context.Background(), gotSkill))

	_, err = r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var dep2 appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep2))
	hash2 := dep2.Annotations["kape.io/rollout-hash"]

	assert.NotEqual(t, hash1, hash2, "rollout hash must change when a referenced skill's spec changes")
}

func TestComputeRolloutHash_ChangesWhenSkillOrderChanges(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skillA := readySkill("a-skill", "kape-system", false, []string{"order-mcp"})
	skillB := readySkill("b-skill", "kape-system", false, []string{"order-mcp"})

	// First reconcile: order = [a, b]
	handler1 := baseKapeHandler("h", "kape-system", "my-schema", nil, "a-skill", "b-skill")
	c1 := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler1, schema, tool, skillA, skillB).
		WithStatusSubresource(handler1, schema, tool, skillA, skillB).
		Build()
	r1 := newReconciler(c1)
	_, err := r1.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var d1 appsv1.Deployment
	require.NoError(t, c1.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &d1))
	hashAB := d1.Annotations["kape.io/rollout-hash"]

	// Second fresh reconcile: order = [b, a]
	handler2 := baseKapeHandler("h", "kape-system", "my-schema", nil, "b-skill", "a-skill")
	c2 := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler2, schema, tool, skillA, skillB).
		WithStatusSubresource(handler2, schema, tool, skillA, skillB).
		Build()
	r2 := newReconciler(c2)
	_, err = r2.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var d2 appsv1.Deployment
	require.NoError(t, c2.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &d2))
	hashBA := d2.Annotations["kape.io/rollout-hash"]

	assert.NotEqual(t, hashAB, hashBA, "rollout hash must change when skill declaration order changes (D13)")
}

func TestHandlerReconciler_ReadyRollup_FalseWhenAnyConditionFalse(t *testing.T) {
	s := newHandlerScheme()
	handler := baseKapeHandler("h", "kape-system", "missing-schema", nil)

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler).
		WithStatusSubresource(handler).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	ready := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
}
