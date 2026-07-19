package reconcile

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildHandlerConditions computes DeploymentAvailable from the Deployment
// status and then folds the entire condition set into the Ready rollup.
//
// Per spec §2.3 the rollup is the NEGATIVE form: Ready=True iff no condition
// in the slice is explicitly False. This is forward-compatible with future
// owners (slice 6's KapeProxyReady, etc.) without changes here.
func buildHandlerConditions(depStatus *appsv1.DeploymentStatus, depFound bool, existing []metav1.Condition) []metav1.Condition {
	deploymentAvailable := metav1.Condition{Type: "DeploymentAvailable"}

	switch {
	case !depFound:
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "DeploymentNotFound"
	case depStatus == nil || depStatus.ReadyReplicas == 0:
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "MinimumReplicasUnavailable"
	default:
		deploymentAvailable.Status = metav1.ConditionTrue
		deploymentAvailable.Reason = "Available"
		deploymentAvailable.Message = fmt.Sprintf("%d/%d replicas ready", depStatus.ReadyReplicas, depStatus.Replicas)
	}
	existing = setCondition(existing, deploymentAvailable)
	existing = setCondition(existing, computeReadyRollup(existing))
	return existing
}

// computeReadyRollup folds every condition in the slice (except "Ready"
// itself) into the Ready rollup using the negative form: Ready=True iff no
// condition is explicitly False. Per spec §2.3 forward-compat rule.
//
// Reason precedence on False: first False condition wins (deterministic via
// slice order). Reason on True is "Ready".
func computeReadyRollup(conditions []metav1.Condition) metav1.Condition {
	for _, c := range conditions {
		if c.Type == "Ready" {
			continue
		}
		if c.Status == metav1.ConditionFalse {
			return metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  c.Reason,
				Message: c.Message,
			}
		}
	}
	return metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: ReasonReady}
}
