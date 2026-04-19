package k8s_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
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
