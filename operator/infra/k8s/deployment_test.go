package k8s_test

import (
	"context"
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

func TestDeploymentAdapter_InjectsSingleKapeproxySidecarForMCPTool(t *testing.T) {
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

	err := adapter.Ensure(context.Background(), handler, cfg, "hash-abc", tools, false)
	require.NoError(t, err)

	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)

	// handler container + exactly 1 kapeproxy sidecar (no kapetool-* containers)
	require.Len(t, dep.Spec.Template.Spec.Containers, 2)

	names := make([]string, len(dep.Spec.Template.Spec.Containers))
	for i, c := range dep.Spec.Template.Spec.Containers {
		names[i] = c.Name
	}
	assert.Contains(t, names, "handler")
	assert.Contains(t, names, "kapeproxy")

	for _, name := range names {
		assert.NotContains(t, name, "kapetool-")
	}
}

func TestDeploymentAdapter_KapeproxySidecar_Resources(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "tool1"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "tool1"},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://x:8080"}}},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	var proxy *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "kapeproxy" {
			proxy = &dep.Spec.Template.Spec.Containers[i]
		}
	}
	require.NotNil(t, proxy, "kapeproxy container must exist")

	assert.True(t, resource.MustParse("100m").Equal(proxy.Resources.Requests[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("128Mi").Equal(proxy.Resources.Requests[corev1.ResourceMemory]))
	assert.True(t, resource.MustParse("500m").Equal(proxy.Resources.Limits[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("256Mi").Equal(proxy.Resources.Limits[corev1.ResourceMemory]))
}

func TestDeploymentAdapter_KapeproxySidecar_VolumeMount(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "tool1"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "tool1"},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://x:8080"}}},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	volumeNames := make([]string, len(dep.Spec.Template.Spec.Volumes))
	for i, v := range dep.Spec.Template.Spec.Volumes {
		volumeNames[i] = v.Name
	}
	assert.Contains(t, volumeNames, "kapeproxy-config")

	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != "kapeproxy" {
			continue
		}
		found := false
		for _, vm := range c.VolumeMounts {
			if vm.Name == "kapeproxy-config" && vm.MountPath == "/etc/kapeproxy" {
				found = true
			}
		}
		assert.True(t, found, "kapeproxy must mount kapeproxy-config at /etc/kapeproxy")
	}
}

func TestDeploymentAdapter_NoSidecarWhenNoMCPTools(t *testing.T) {
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
	err := k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "hash-123", tools, false)
	require.NoError(t, err)

	var dep appsv1.Deployment
	_ = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)

	assert.Len(t, dep.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "handler", dep.Spec.Template.Spec.Containers[0].Name)

	for _, v := range dep.Spec.Template.Spec.Volumes {
		assert.NotEqual(t, "kapeproxy-config", v.Name)
	}
}

func TestDeploymentAdapter_HandlerResources_DefaultsWhenSpecUnset(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	require.NoError(t, adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil, false))

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

	require.NoError(t, adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	got := dep.Spec.Template.Spec.Containers[0].Resources
	assert.True(t, override.Requests[corev1.ResourceCPU].Equal(got.Requests[corev1.ResourceCPU]))
	assert.True(t, override.Requests[corev1.ResourceMemory].Equal(got.Requests[corev1.ResourceMemory]))
	assert.True(t, override.Limits[corev1.ResourceCPU].Equal(got.Limits[corev1.ResourceCPU]))
	assert.True(t, override.Limits[corev1.ResourceMemory].Equal(got.Limits[corev1.ResourceMemory]))
}
