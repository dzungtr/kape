package reconcile

import (
	"context"
	"fmt"
	"net/http"
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
	tools       ports.ToolRepository
	statefulSet ports.StatefulSetPort
	kapeConfig  ports.KapeConfigLoader
}

// NewToolReconciler creates a ToolReconciler.
func NewToolReconciler(
	tools ports.ToolRepository,
	statefulSet ports.StatefulSetPort,
	kapeConfig ports.KapeConfigLoader,
) *ToolReconciler {
	return &ToolReconciler{tools: tools, statefulSet: statefulSet, kapeConfig: kapeConfig}
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
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}

	if err := r.statefulSet.EnsureQdrant(ctx, tool, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Qdrant: %w", err)
	}

	stsKey := types.NamespacedName{Name: "kape-memory-" + tool.Name, Namespace: tool.Namespace}
	readyReplicas, found, err := r.statefulSet.GetQdrantReadyReplicas(ctx, stsKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Qdrant status: %w", err)
	}

	if !found || readyReplicas < 1 {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "QdrantNotReady",
			Message: "StatefulSet has no ready replicas",
		})
		tool.Status.QdrantEndpoint = ""
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	endpoint := fmt.Sprintf("http://kape-memory-%s.%s:6333", tool.Name, tool.Namespace)
	tool.Status.QdrantEndpoint = endpoint
	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "Qdrant StatefulSet ready",
	})
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ToolReconciler) reconcileMCP(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
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
