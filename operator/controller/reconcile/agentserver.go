package reconcile

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/kape-io/kape/operator/domain/agentserver"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// Reasons reported on the KapeAgentServer Ready condition.
const (
	ReasonDeployed          = "Deployed"
	ReasonProgressing       = "Progressing"
	ReasonWiringIncomplete  = "WiringIncomplete"
	ReasonOwnershipConflict = "OwnershipConflict"
	ReasonRenderFailed      = "RenderFailed"
	ReasonApplyFailed       = "ApplyFailed"
)

// AgentServerReconciler reconciles a KapeAgentServer into a kape-controller
// Deployment plus its network resources (spec #261 M5).
//
// The split is deliberate: agentserver.Render is pure and produces the env
// contract, while this reconciler is the impure half — it merges the
// operator-level cluster wiring, applies, and reports the outcome on status.
type AgentServerReconciler struct {
	servers ports.AgentServerRepository
	applier ports.AgentServerApplyPort
	wiring  domainconfig.ControllerWiring
}

// NewAgentServerReconciler creates an AgentServerReconciler.
func NewAgentServerReconciler(
	servers ports.AgentServerRepository,
	applier ports.AgentServerApplyPort,
	wiring domainconfig.ControllerWiring,
) *AgentServerReconciler {
	return &AgentServerReconciler{servers: servers, applier: applier, wiring: wiring}
}

// Reconcile renders the CR and converges its objects:
//
//  1. Fetch KapeAgentServer (no-op when gone)
//  2. Render DesiredState (pure)
//  3. Gate on cluster wiring — an unset wiring var is reported, never defaulted
//  4. Apply the controller Deployment + network resources
//  5. Read Deployment availability → Ready condition
func (r *AgentServerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	server, err := r.servers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeAgentServer: %w", err)
	}
	if server == nil {
		return ctrl.Result{}, nil
	}
	if !server.DeletionTimestamp.IsZero() {
		// Rendered objects are owner-referenced, so the garbage collector
		// removes them with the CR; nothing else to clean up.
		return ctrl.Result{}, nil
	}

	desired := agentserver.Render(server, nil)

	if missing := r.wiring.Missing(); len(missing) > 0 {
		return r.setReady(ctx, server, metav1.ConditionFalse, ReasonWiringIncomplete,
			fmt.Sprintf("operator cluster wiring incomplete: set %s on the kape-operator Deployment",
				strings.Join(missing, ", ")))
	}

	if err := r.applier.Apply(ctx, server, desired, r.wiring); err != nil {
		if errors.Is(err, ports.ErrNotOwned) {
			return r.setReady(ctx, server, metav1.ConditionFalse, ReasonOwnershipConflict, err.Error())
		}
		// Record the failure, then return it so the reconcile is retried.
		_, _ = r.setReady(ctx, server, metav1.ConditionFalse, ReasonApplyFailed, err.Error())
		return ctrl.Result{}, fmt.Errorf("applying rendered objects: %w", err)
	}

	available, found, err := r.applier.DeploymentStatus(ctx, types.NamespacedName{
		Namespace: server.Namespace,
		Name:      agentserver.ControllerName(server.Name),
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	name := agentserver.ControllerName(server.Name)
	switch {
	case !found:
		return r.setReady(ctx, server, metav1.ConditionFalse, ReasonProgressing,
			fmt.Sprintf("controller Deployment %s/%s has not been created yet", server.Namespace, name))
	case !available:
		return r.setReady(ctx, server, metav1.ConditionFalse, ReasonProgressing,
			fmt.Sprintf("controller Deployment %s/%s is not available yet", server.Namespace, name))
	}
	return r.setReady(ctx, server, metav1.ConditionTrue, ReasonDeployed,
		fmt.Sprintf("controller Deployment %s/%s is available", server.Namespace, name))
}

// setReady records the Ready condition and the observed generation, then
// persists status. Status errors are returned so the next reconcile retries.
func (r *AgentServerReconciler) setReady(
	ctx context.Context,
	server *v1alpha1.KapeAgentServer,
	status metav1.ConditionStatus,
	reason, message string,
) (ctrl.Result, error) {
	server.Status.Conditions = setCondition(server.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: server.Generation,
	})
	server.Status.ObservedGeneration = server.Generation
	if err := r.servers.UpdateStatus(ctx, server); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
