package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// ToolReconciler performs the full reconcile logic for KapeTool.
type ToolReconciler struct {
	tools          ports.ToolRepository
	qdrantCluster  ports.QdrantClusterPort
}

// NewToolReconciler creates a ToolReconciler.
func NewToolReconciler(
	tools ports.ToolRepository,
	qdrantCluster ports.QdrantClusterPort,
) *ToolReconciler {
	return &ToolReconciler{tools: tools, qdrantCluster: qdrantCluster}
}

// Reconcile dispatches on spec.type.
func (r *ToolReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	tool, err := r.tools.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeTool: %w", err)
	}
	if tool == nil {
		return ctrl.Result{}, nil
	}

	switch tool.Spec.Type {
	case "memory":
		return r.reconcileMemory(ctx, tool)
	case "mcp":
		return r.reconcileMCP(ctx, tool)
	case "event-publish":
		return r.reconcileEventPublish(ctx, tool)
	default:
		return ctrl.Result{}, nil
	}
}

func (r *ToolReconciler) reconcileMemory(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	if tool.Spec.Memory != nil && tool.Spec.Memory.External != nil {
		return r.reconcileExternalMemory(ctx, tool)
	}
	return r.reconcileProvisionedMemory(ctx, tool)
}

// reconcileExternalMemory handles memory tools that point at a user-managed database.
// It skips Qdrant provisioning and publishes the external URL into status.QdrantEndpoint
// so downstream consumers (TOML renderer, handler env injection) can find it in the
// same field they already read.
func (r *ToolReconciler) reconcileExternalMemory(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	ext := tool.Spec.Memory.External
	if !hasHTTPScheme(ext.URL) {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidExternalURL",
			Message: "spec.memory.external.url must start with http:// or https://",
		})
		tool.Status.QdrantEndpoint = ""
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, fmt.Errorf("invalid external URL %q: must start with http:// or https://", ext.URL)
	}
	tool.Status.QdrantEndpoint = ext.URL
	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "ExternalDatabase",
		Message: "Using external database; provisioning skipped",
	})
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// reconcileProvisionedMemory delegates Qdrant provisioning to the upstream
// qdrant-operator by creating a QdrantCluster CRD object, then polls its
// status for readiness. If the operator CRD is not installed, it surfaces a
// MemoryReady=False/OperatorNotInstalled condition.
func (r *ToolReconciler) reconcileProvisionedMemory(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	installed, err := r.qdrantCluster.IsCRDInstalled(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking QdrantCluster CRD: %w", err)
	}
	if !installed {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "MemoryReady",
			Status:  metav1.ConditionFalse,
			Reason:  "OperatorNotInstalled",
			Message: "QdrantCluster.qdrant.io CRD not found — install the qdrant-operator",
		})
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "OperatorNotInstalled",
			Message: "QdrantCluster.qdrant.io CRD not found — install the qdrant-operator",
		})
		tool.Status.QdrantEndpoint = ""
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if err := r.qdrantCluster.EnsureQdrantCluster(ctx, tool); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring QdrantCluster: %w", err)
	}

	clusterKey := types.NamespacedName{Name: "kape-memory-" + tool.Name, Namespace: tool.Namespace}
	ready, url, found, err := r.qdrantCluster.GetQdrantClusterStatus(ctx, clusterKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading QdrantCluster status: %w", err)
	}

	if !found || !ready {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "MemoryReady",
			Status:  metav1.ConditionFalse,
			Reason:  "QdrantClusterNotReady",
			Message: "QdrantCluster has not reached Ready=True",
		})
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "QdrantClusterNotReady",
			Message: "QdrantCluster has not reached Ready=True",
		})
		tool.Status.QdrantEndpoint = ""
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	tool.Status.QdrantEndpoint = url
	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "MemoryReady",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "QdrantCluster ready",
	})
	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "QdrantCluster ready",
	})
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ToolReconciler) reconcileMCP(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	if tool.Spec.MCP.SkipProbe {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "ProbeSkipped",
			Message: "MCP health probe disabled via spec.mcp.skipProbe",
		})
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	err := probeMCPEndpoint(tool.Spec.MCP.Upstream.URL)
	if err != nil {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "MCPEndpointUnreachable",
			Message: fmt.Sprintf("Health probe failed: %v", err),
		})
	} else {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "MCP endpoint reachable",
		})
	}
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ToolReconciler) reconcileEventPublish(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	ep := tool.Spec.EventPublish
	if ep == nil || !strings.HasPrefix(ep.Type, "kape.events.") {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ValidationFailed",
			Message: "spec.eventPublish.type must start with 'kape.events.'",
		})
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // terminal — no requeue
	}

	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "event-publish type valid",
	})
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// probeMCPEndpoint performs an HTTP GET to {url}/health with 5s timeout, 3 attempts.
func probeMCPEndpoint(rawURL string) error {
	healthURL := strings.TrimRight(rawURL, "/") + "/health"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for i := 0; i < 3; i++ {
		resp, err := httpClient.Get(healthURL) //nolint:noctx
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return lastErr
}

// setCondition upserts a condition by type, preserving LastTransitionTime when status is unchanged.
func setCondition(conditions []metav1.Condition, c metav1.Condition) []metav1.Condition {
	c.LastTransitionTime = metav1.Now()
	for i, existing := range conditions {
		if existing.Type == c.Type {
			if existing.Status == c.Status {
				c.LastTransitionTime = existing.LastTransitionTime
			}
			conditions[i] = c
			return conditions
		}
	}
	return append(conditions, c)
}


// hasHTTPScheme reports whether u has an http or https scheme. The CRD
// enforces this via a validation pattern at admission; this check provides
// defense in depth for tools that bypass admission (e.g. tests, direct API
// writes) and produces a clear reconciler error.
func hasHTTPScheme(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}
