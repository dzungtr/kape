package reconcile

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kape-io/kape/operator/infra/ports"
)

// ProxyHealth classifies the observed state of the kapeproxy container.
type ProxyHealth int

const (
	ProxyHealthy   ProxyHealth = iota // container is Running and Ready
	ProxyCrashLoop                    // container is in CrashLoopBackOff
	ProxyNotReady                     // container exists but not ready (other reason)
	ProxyMissing                      // no kapeproxy container found in Pod
)

const kapeproxyContainerName = "kapeproxy"

// KapeProxyReconciler watches Pods labelled kape.io/handler=* and writes
// KapeProxyReady / KapeProxyDegraded conditions on the parent KapeHandler.
// It is observability-only: it never mutates Deployments or ConfigMaps.
type KapeProxyReconciler struct {
	pods     ports.PodReader
	handlers ports.HandlerRepository
}

// NewKapeProxyReconciler creates a KapeProxyReconciler.
func NewKapeProxyReconciler(pods ports.PodReader, handlers ports.HandlerRepository) *KapeProxyReconciler {
	return &KapeProxyReconciler{pods: pods, handlers: handlers}
}

// Reconcile receives a Pod event, classifies kapeproxy health, and writes conditions
// on the parent KapeHandler.
func (r *KapeProxyReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("pod", key)

	pod, err := r.pods.GetPod(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching pod %s: %w", key, err)
	}
	if pod == nil {
		return ctrl.Result{}, nil // Pod deleted
	}

	handlerName, ok := pod.Labels["kape.io/handler"]
	if !ok || handlerName == "" {
		log.V(1).Info("pod missing kape.io/handler label, skipping")
		return ctrl.Result{}, nil
	}

	health := EvaluatePodHealth(pod)

	handlerKey := types.NamespacedName{Name: handlerName, Namespace: pod.Namespace}
	handler, err := r.handlers.Get(ctx, handlerKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler %s: %w", handlerKey, err)
	}
	if handler == nil {
		log.V(1).Info("KapeHandler not found, skipping", "handler", handlerKey)
		return ctrl.Result{}, nil
	}

	handler.Status.Conditions = applyProxyConditions(handler.Status.Conditions, health)
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating KapeHandler status: %w", err)
	}

	return ctrl.Result{}, nil
}

// EvaluatePodHealth inspects the kapeproxy container status and returns a ProxyHealth value.
// Exported so it can be unit-tested independently of reconcile wiring.
func EvaluatePodHealth(pod *corev1.Pod) ProxyHealth {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != kapeproxyContainerName {
			continue
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return ProxyCrashLoop
		}
		if cs.Ready {
			return ProxyHealthy
		}
		return ProxyNotReady
	}
	return ProxyMissing
}

// applyProxyConditions sets KapeProxyReady and KapeProxyDegraded based on the health result.
func applyProxyConditions(conditions []metav1.Condition, health ProxyHealth) []metav1.Condition {
	switch health {
	case ProxyHealthy:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "kapeproxy container is running and ready",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionFalse,
			Reason:  "Healthy",
			Message: "kapeproxy container is healthy",
		})
	case ProxyCrashLoop:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ContainerCrashLoop",
			Message: "kapeproxy container is in CrashLoopBackOff",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionTrue,
			Reason:  "RestartLoop",
			Message: "kapeproxy container is crash-looping",
		})
	case ProxyNotReady:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "ContainerNotReady",
			Message: "kapeproxy container exists but is not ready",
		})
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyDegraded",
			Status:  metav1.ConditionFalse,
			Reason:  "Healthy",
			Message: "kapeproxy container is not yet ready",
		})
	case ProxyMissing:
		conditions = setCondition(conditions, metav1.Condition{
			Type:    "KapeProxyReady",
			Status:  metav1.ConditionFalse,
			Reason:  "KapeProxyMissing",
			Message: "no kapeproxy container found in pod",
		})
		// KapeProxyDegraded is intentionally not written for Missing
	}
	return conditions
}
