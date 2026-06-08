//go:build envtest

package integration_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// uniqueNS creates a fresh namespace for each test and registers DeferCleanup to delete it.
func uniqueNS(suffix string) string {
	ns := fmt.Sprintf("test-%s", suffix)
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	Expect(k8sClient.Create(ctx, nsObj)).To(Succeed())
	DeferCleanup(func(dctx context.Context) {
		_ = k8sClient.Delete(dctx, nsObj, client.GracePeriodSeconds(0))
	})
	return ns
}

// readySchema builds a KapeSchema with a valid spec (all CRD admission constraints satisfied):
// - type=object (CEL rule)
// - required has at least one entry (kubebuilder:validation:MinItems=1)
// - properties is present (required field in CRD spec)
// - additionalProperties=false (CEL rule)
// - required field names exist in properties (enforced by reconciler, not admission)
func readySchema(name, ns string) *v1alpha1.KapeSchema {
	addlProps := false
	return &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
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
}

// minimalHandler builds a KapeHandler that references the given schema and has no tools or skills.
func minimalHandler(name, ns, schemaRef string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: schemaRef,
			Tools:     []v1alpha1.ToolRef{},
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
}

// eventPublishTool builds a valid event-publish KapeTool.
func eventPublishTool(name, ns string) *v1alpha1.KapeTool {
	return &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeToolSpec{
			Type:         "event-publish",
			EventPublish: &v1alpha1.EventPublishSpec{Type: "kape.events.alert"},
		},
	}
}

// mcpToolSkipProbe builds a KapeTool of type mcp with skipProbe=true.
func mcpToolSkipProbe(name, ns string) *v1alpha1.KapeTool {
	return &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream:  v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://some-mcp-server"},
				SkipProbe: true,
			},
		},
	}
}

// findCondition finds a condition by type in the slice, or returns nil.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
