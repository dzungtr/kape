package k8s_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func TestDeploymentAdapter_InjectsSidecarForMCPTool(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "test-handler", Namespace: "kape-system", UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Tools: []v1alpha1.ToolRef{{Ref: "grafana-mcp"}},
		},
	}
	auditEnabled := true
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream:     v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://grafana:8080"},
				AllowedTools: []string{"grafana_query"},
				Audit:        &v1alpha1.AuditSpec{Enabled: &auditEnabled},
			},
		},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)
	cfg := domainconfig.KapeConfig{}

	err := adapter.Ensure(context.Background(), handler, cfg, "hash-abc", tools)
	require.NoError(t, err)

	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)

	// handler container + 1 sidecar
	assert.Len(t, dep.Spec.Template.Spec.Containers, 2)

	sidecar := dep.Spec.Template.Spec.Containers[1]
	assert.Equal(t, "kapetool-grafana-mcp", sidecar.Name)

	envMap := make(map[string]string)
	for _, e := range sidecar.Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, "http://grafana:8080", envMap["KAPETOOL_UPSTREAM_URL"])
	assert.Equal(t, "sse", envMap["KAPETOOL_UPSTREAM_TRANSPORT"])

	var allowedTools []string
	_ = json.Unmarshal([]byte(envMap["KAPETOOL_ALLOWED_TOOLS"]), &allowedTools)
	assert.Equal(t, []string{"grafana_query"}, allowedTools)
}

func TestDeploymentAdapter_HandlerResources_DefaultsWhenSpecUnset(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	require.NoError(t, adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	got := dep.Spec.Template.Spec.Containers[0].Resources
	assert.True(t, resource.MustParse("100m").Equal(got.Requests[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("128Mi").Equal(got.Requests[corev1.ResourceMemory]))
	assert.True(t, resource.MustParse("500m").Equal(got.Limits[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("512Mi").Equal(got.Limits[corev1.ResourceMemory]))
}

func TestDeploymentAdapter_HandlerResources_OverrideFromSpec(t *testing.T) {
	override := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Resources: override},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	require.NoError(t, adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	got := dep.Spec.Template.Spec.Containers[0].Resources
	assert.True(t, override.Requests[corev1.ResourceCPU].Equal(got.Requests[corev1.ResourceCPU]))
	assert.True(t, override.Requests[corev1.ResourceMemory].Equal(got.Requests[corev1.ResourceMemory]))
	assert.True(t, override.Limits[corev1.ResourceCPU].Equal(got.Limits[corev1.ResourceCPU]))
	assert.True(t, override.Limits[corev1.ResourceMemory].Equal(got.Limits[corev1.ResourceMemory]))
}

func TestDeploymentAdapter_NoSidecarForMemoryTool(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "test-handler", Namespace: "kape-system", UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Tools: []v1alpha1.ToolRef{{Ref: "my-memory"}},
		},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "my-memory"},
		Spec:       v1alpha1.KapeToolSpec{Type: "memory"},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	err := adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "hash-123", tools)
	require.NoError(t, err)

	var dep appsv1.Deployment
	_ = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)

	// handler container only — no sidecar for memory type
	assert.Len(t, dep.Spec.Template.Spec.Containers, 1)
}
