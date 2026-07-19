package k8s_test

import (
	"context"
	"strings"
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

func TestBuildDeployment_MemoryToolVolume(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "order-memory"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "order-memory"},
		Spec:       v1alpha1.KapeToolSpec{Type: "memory"},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	// Volume assertions
	var foundVol *corev1.Volume
	for i := range dep.Spec.Template.Spec.Volumes {
		if dep.Spec.Template.Spec.Volumes[i].Name == "kape-tool-order-memory-secrets" {
			foundVol = &dep.Spec.Template.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, foundVol, "expected volume kape-tool-order-memory-secrets")
	require.NotNil(t, foundVol.VolumeSource.Secret, "volume must use Secret source")
	assert.Equal(t, "kape-tool-order-memory-conn", foundVol.VolumeSource.Secret.SecretName)

	// Find handler container
	var handlerCtr *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "handler" {
			handlerCtr = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, handlerCtr, "handler container must exist")

	// VolumeMount assertions
	var foundVM *corev1.VolumeMount
	for i := range handlerCtr.VolumeMounts {
		if handlerCtr.VolumeMounts[i].Name == "kape-tool-order-memory-secrets" {
			foundVM = &handlerCtr.VolumeMounts[i]
			break
		}
	}
	require.NotNil(t, foundVM, "expected VolumeMount kape-tool-order-memory-secrets")
	assert.Equal(t, "/etc/kape/secrets/order-memory", foundVM.MountPath)
	assert.True(t, foundVM.ReadOnly, "VolumeMount must be ReadOnly")

	// Env var assertions
	var kapeToolName, qdrantURL, qdrantCollection string
	for _, e := range handlerCtr.Env {
		switch e.Name {
		case "KAPE_TOOL_NAME":
			kapeToolName = e.Value
		case "QDRANT_URL":
			qdrantURL = e.Value
		case "QDRANT_COLLECTION":
			qdrantCollection = e.Value
		}
	}
	assert.Equal(t, "order-memory", kapeToolName, "KAPE_TOOL_NAME must be set to the memory tool name")
	assert.Empty(t, qdrantURL, "QDRANT_URL must not be injected")
	assert.Empty(t, qdrantCollection, "QDRANT_COLLECTION must not be injected")
}

func TestBuildDeployment_ExternalMemorySecretRef_InjectsAPIKeyEnv(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "ext-mem"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-mem"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "memory",
			Memory: &v1alpha1.MemorySpec{
				Backend:        "qdrant",
				DistanceMetric: "cosine",
				External: &v1alpha1.ExternalMemorySpec{
					URL:       "http://my-qdrant.example.com:6333",
					SecretRef: &corev1.LocalObjectReference{Name: "my-qdrant-api-key"},
				},
			},
		},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	var handlerCtr *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "handler" {
			handlerCtr = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, handlerCtr, "handler container must exist")

	var apiKeyEnv *corev1.EnvVar
	for i := range handlerCtr.Env {
		if handlerCtr.Env[i].Name == "QDRANT_API_KEY" {
			apiKeyEnv = &handlerCtr.Env[i]
			break
		}
	}
	require.NotNil(t, apiKeyEnv, "QDRANT_API_KEY env var must be injected when external.secretRef is set")
	require.NotNil(t, apiKeyEnv.ValueFrom, "QDRANT_API_KEY must use ValueFrom")
	require.NotNil(t, apiKeyEnv.ValueFrom.SecretKeyRef, "QDRANT_API_KEY must reference a SecretKeySelector")
	assert.Equal(t, "my-qdrant-api-key", apiKeyEnv.ValueFrom.SecretKeyRef.Name, "Secret name must match external.secretRef.name")
	assert.Equal(t, "apiKey", apiKeyEnv.ValueFrom.SecretKeyRef.Key, "default Secret key must be apiKey")
}

func TestBuildDeployment_ExternalMemoryNoSecretRef_OmitsAPIKeyEnv(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "ext-mem"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-mem"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "memory",
			Memory: &v1alpha1.MemorySpec{
				Backend:        "qdrant",
				DistanceMetric: "cosine",
				External:       &v1alpha1.ExternalMemorySpec{URL: "http://my-qdrant.example.com:6333"},
			},
		},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	var handlerCtr *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "handler" {
			handlerCtr = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, handlerCtr, "handler container must exist")

	for _, e := range handlerCtr.Env {
		assert.NotEqual(t, "QDRANT_API_KEY", e.Name, "QDRANT_API_KEY must not be injected when external.secretRef is absent")
	}
}

func TestBuildDeployment_NoMemoryTool(t *testing.T) {
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
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "hash-abc", tools, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep))

	// No kape-tool- volumes
	for _, v := range dep.Spec.Template.Spec.Volumes {
		assert.False(t, strings.HasPrefix(v.Name, "kape-tool-"),
			"unexpected kape-tool- volume: %s", v.Name)
	}

	// Find handler container
	var handlerCtr *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "handler" {
			handlerCtr = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, handlerCtr, "handler container must exist")

	// No kape-tool- VolumeMounts
	for _, vm := range handlerCtr.VolumeMounts {
		assert.False(t, strings.HasPrefix(vm.Name, "kape-tool-"),
			"unexpected kape-tool- VolumeMount: %s", vm.Name)
	}

	// No KAPE_TOOL_NAME env var
	for _, e := range handlerCtr.Env {
		assert.NotEqual(t, "KAPE_TOOL_NAME", e.Name, "KAPE_TOOL_NAME must not be set when no memory tool is present")
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

func TestDeploymentAdapter_NATSCertWiring(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil, false))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	// (a) Pod spec must contain a Volume named "nats-certs" backed by kape-handler-cert Secret.
	var natsCertsVolume *corev1.Volume
	for i := range dep.Spec.Template.Spec.Volumes {
		if dep.Spec.Template.Spec.Volumes[i].Name == "nats-certs" {
			natsCertsVolume = &dep.Spec.Template.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, natsCertsVolume, "volume nats-certs must exist in pod spec")
	require.NotNil(t, natsCertsVolume.VolumeSource.Secret, "nats-certs volume must be a Secret volume")
	assert.Equal(t, "kape-handler-cert", natsCertsVolume.VolumeSource.Secret.SecretName)

	// (b) Handler container must have a VolumeMount named "nats-certs".
	var handlerContainer *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "handler" {
			handlerContainer = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, handlerContainer, "handler container must exist")

	var natsCertsMount *corev1.VolumeMount
	for i := range handlerContainer.VolumeMounts {
		if handlerContainer.VolumeMounts[i].Name == "nats-certs" {
			natsCertsMount = &handlerContainer.VolumeMounts[i]
			break
		}
	}
	require.NotNil(t, natsCertsMount, "handler container must have VolumeMount nats-certs")
	assert.Equal(t, "/etc/kape/nats-certs", natsCertsMount.MountPath)
	assert.True(t, natsCertsMount.ReadOnly)

	// (c) Handler container must export NATS_TLS_CERT, NATS_TLS_KEY, NATS_TLS_CA.
	envMap := make(map[string]string, len(handlerContainer.Env))
	for _, e := range handlerContainer.Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, "/etc/kape/nats-certs/tls.crt", envMap["NATS_TLS_CERT"])
	assert.Equal(t, "/etc/kape/nats-certs/tls.key", envMap["NATS_TLS_KEY"])
	assert.Equal(t, "/etc/kape/nats-certs/ca.crt", envMap["NATS_TLS_CA"])
}
