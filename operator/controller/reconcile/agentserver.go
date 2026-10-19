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

// Reasons reported on the KapeAgentServer Ready and Degraded conditions.
const (
	ReasonDeployed          = "Deployed"
	ReasonProgressing       = "Progressing"
	ReasonWiringIncomplete  = "WiringIncomplete"
	ReasonOwnershipConflict = "OwnershipConflict"
	ReasonRenderFailed      = "RenderFailed"
	ReasonApplyFailed       = "ApplyFailed"

	ReasonCredentialsCurrent  = "CredentialsCurrent"
	ReasonNoCredentials       = "NoCredentials"
	ReasonCredentialsDegraded = "CredentialsDegraded"
)

// Condition types the reconciler owns on a KapeAgentServer.
const (
	// ConditionReady is the aggregate rollup of the rendered objects.
	ConditionReady = "Ready"
	// ConditionDegraded is the aggregate credential rollup: True when at least
	// one credential could not be provisioned. It never implies the rest of the
	// profile is down — a broken credential degrades only itself (D9).
	ConditionDegraded = "Degraded"
	// ConditionPushed is the per-credential provisioning condition.
	ConditionPushed = "Pushed"
	// ConditionFailClosedEnforced records the gateway-side gap in handoff H6:
	// on_error: fail_closed has no endpoint-level representation on OpenShell
	// v0.1.0, so the controller enforces fail-closed on its own side.
	ConditionFailClosedEnforced = "FailClosedEnforced"
)

// ReasonFailClosedUnavailable explains ConditionFailClosedEnforced=False.
const ReasonFailClosedUnavailable = "GatewayEnforcementUnavailable"

// failClosedMessage is the H6 gap statement carried on every pushed credential.
const failClosedMessage = "on_error: fail_closed has no endpoint-level representation on OpenShell v0.1.0 " +
	"(NetworkMiddlewareConfig is policy-level and MergeOperations carries no middleware op); " +
	"fail-closed is enforced controller-side: a binding that cannot be materialised aborts the apply (handoff H6)"

// AgentServerReconciler reconciles a KapeAgentServer into a kape-controller
// Deployment plus its network resources and its OpenShell credential records
// (spec #261 M5).
//
// The split is deliberate: agentserver.Render is pure and produces the env
// contract, while this reconciler is the impure half — it resolves Secrets,
// pushes provider records, merges the operator-level cluster wiring, applies,
// and reports the outcome on status.
type AgentServerReconciler struct {
	servers  ports.AgentServerRepository
	applier  ports.AgentServerApplyPort
	resolver ports.SecretResolver
	writer   ports.OpenShellApplyPort
	wiring   domainconfig.ControllerWiring
}

// NewAgentServerReconciler creates an AgentServerReconciler. writer is nil when
// the operator has no OpenShell gateway configured: every credential then reports
// Pushed=False/GatewayNotConfigured instead of pretending to be provisioned.
func NewAgentServerReconciler(
	servers ports.AgentServerRepository,
	applier ports.AgentServerApplyPort,
	resolver ports.SecretResolver,
	writer ports.OpenShellApplyPort,
	wiring domainconfig.ControllerWiring,
) *AgentServerReconciler {
	return &AgentServerReconciler{servers: servers, applier: applier, resolver: resolver, writer: writer, wiring: wiring}
}

// Reconcile renders the CR and converges its objects:
//
//  1. Fetch KapeAgentServer (no-op when gone)
//  2. Resolve every credential Secret (same-namespace, label-gated)
//  3. Push the provider records the resolver produced (import → create/update)
//  4. Render again with the pushed credentials only — KAPE_CREDENTIAL_BINDINGS
//     never names a provider the gateway does not hold
//  5. Gate on cluster wiring, apply the controller Deployment + network
//     resources, and read Deployment availability
//  6. Report Ready, the aggregate Degraded condition, and per-credential state
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

	resolved, resolverFailures := r.resolveCredentials(ctx, server)
	outcomes, gatewayErr := r.pushCredentials(ctx, agentserver.Render(server, resolved).OpenShellRecords)
	credentialOutcomes := mergeOutcomes(server, resolverFailures, outcomes)
	desired := agentserver.Render(server, pushedSecrets(resolved, outcomes))

	if missing := r.wiring.Missing(); len(missing) > 0 {
		return r.setReady(ctx, server, credentialOutcomes, readyCondition(server, metav1.ConditionFalse, ReasonWiringIncomplete,
			fmt.Sprintf("operator cluster wiring incomplete: set %s on the kape-operator Deployment", strings.Join(missing, ", "))))
	}

	if err := r.applier.Apply(ctx, server, desired, r.wiring); err != nil {
		if errors.Is(err, ports.ErrNotOwned) {
			return r.setReady(ctx, server, credentialOutcomes, readyCondition(server, metav1.ConditionFalse, ReasonOwnershipConflict, err.Error()))
		}
		// Record the failure, then return it so the reconcile is retried.
		_, _ = r.setReady(ctx, server, credentialOutcomes, readyCondition(server, metav1.ConditionFalse, ReasonApplyFailed, err.Error()))
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
	var ready metav1.Condition
	switch {
	case !found:
		ready = readyCondition(server, metav1.ConditionFalse, ReasonProgressing,
			fmt.Sprintf("controller Deployment %s/%s has not been created yet", server.Namespace, name))
	case !available:
		ready = readyCondition(server, metav1.ConditionFalse, ReasonProgressing,
			fmt.Sprintf("controller Deployment %s/%s is not available yet", server.Namespace, name))
	default:
		ready = readyCondition(server, metav1.ConditionTrue, ReasonDeployed,
			fmt.Sprintf("controller Deployment %s/%s is available", server.Namespace, name))
	}
	result, statusErr := r.setReady(ctx, server, credentialOutcomes, ready)
	if gatewayErr != nil {
		// The Deployment converged; the gateway did not. Surface the credential
		// write so the reconcile is retried with backoff without rolling back
		// the healthy part of the profile (D9).
		return result, fmt.Errorf("pushing credential providers: %w", gatewayErr)
	}
	return result, statusErr
}

// resolveCredentials reads every credential Secret and reports the entries that
// could not be used. A cross-namespace secretRef is refused before any read:
// admission (CEL) rejects it, and this is the defence in depth for objects that
// bypassed admission (D10).
func (r *AgentServerReconciler) resolveCredentials(
	ctx context.Context,
	server *v1alpha1.KapeAgentServer,
) (agentserver.ResolvedSecrets, map[string]agentserver.CredentialFailure) {
	failures := map[string]agentserver.CredentialFailure{}
	refs := make([]ports.CredentialSecretRef, 0, len(server.Spec.Credentials))
	for _, cred := range server.Spec.Credentials {
		if cred.SecretRef.Namespace != "" {
			failures[cred.Name] = agentserver.CredentialFailure{
				Reason: agentserver.ReasonCrossNamespaceSecretRef,
				Message: fmt.Sprintf("credentials[%q].secretRef names namespace %q; credential Secrets are read from %q only",
					cred.Name, cred.SecretRef.Namespace, server.Namespace),
			}
			continue
		}
		refs = append(refs, ports.CredentialSecretRef{Credential: cred.Name, Secret: cred.SecretRef.Name})
	}
	resolved, resolverFailures := r.resolver.Resolve(ctx, server.Namespace, refs)
	for credential, failure := range resolverFailures {
		failures[credential] = failure
	}
	return resolved, failures
}

// pushCredentials converges the gateway-side provider records. Without a
// configured gateway every credential is reported, never silently skipped.
func (r *AgentServerReconciler) pushCredentials(
	ctx context.Context,
	records []agentserver.OpenShellRecord,
) ([]agentserver.CredentialOutcome, error) {
	if len(records) == 0 {
		return nil, nil
	}
	if r.writer == nil {
		outcomes := make([]agentserver.CredentialOutcome, 0, len(records))
		for _, rec := range records {
			outcomes = append(outcomes, agentserver.FailedOutcome(rec.Credential, rec.Name, agentserver.CredentialFailure{
				Reason:  agentserver.ReasonGatewayNotConfigured,
				Message: "operator has no OpenShell gateway configured (KAPE_OPERATOR_OPENSHHELL_GATEWAY_ADDR); cannot push provider records",
			}))
		}
		return outcomes, nil
	}
	return r.writer.Apply(ctx, records)
}

// mergeOutcomes folds resolver and writer results into one per-credential map in
// CR order. A credential the resolver rejected never reaches the writer, so the
// two sets are disjoint; the merge is the union.
func mergeOutcomes(
	server *v1alpha1.KapeAgentServer,
	resolverFailures map[string]agentserver.CredentialFailure,
	writerOutcomes []agentserver.CredentialOutcome,
) map[string]agentserver.CredentialOutcome {
	spec := server.Spec.Credentials
	outcomes := make(map[string]agentserver.CredentialOutcome, len(spec))
	for _, cred := range spec {
		if failure, failed := resolverFailures[cred.Name]; failed {
			provider := agentserver.ProviderName(server.Namespace, server.Name, cred.Name)
			outcomes[cred.Name] = agentserver.FailedOutcome(cred.Name, provider, failure)
		}
	}
	for _, outcome := range writerOutcomes {
		outcomes[outcome.Credential] = outcome
	}
	return outcomes
}

// pushedSecrets keeps only the credentials the gateway accepted: the renderer
// derives KAPE_CREDENTIAL_BINDINGS from exactly this set.
func pushedSecrets(
	resolved agentserver.ResolvedSecrets,
	outcomes []agentserver.CredentialOutcome,
) agentserver.ResolvedSecrets {
	pushed := agentserver.ResolvedSecrets{}
	for _, outcome := range outcomes {
		if !outcome.Pushed {
			continue
		}
		if values, ok := resolved[outcome.Credential]; ok {
			pushed[outcome.Credential] = values
		}
	}
	return pushed
}

// readyCondition builds the Ready condition for this generation.
func readyCondition(server *v1alpha1.KapeAgentServer, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: server.Generation,
	}
}

// setReady records Ready, the aggregate Degraded condition, and the
// per-credential conditions, then persists status. Status errors are returned so
// the next reconcile retries.
func (r *AgentServerReconciler) setReady(
	ctx context.Context,
	server *v1alpha1.KapeAgentServer,
	outcomes map[string]agentserver.CredentialOutcome,
	ready metav1.Condition,
) (ctrl.Result, error) {
	server.Status.Conditions = setCondition(server.Status.Conditions, ready)
	server.Status.Conditions = setCondition(server.Status.Conditions,
		degradedCondition(server.Spec.Credentials, outcomes))
	server.Status.Credentials = credentialsStatus(server.Spec.Credentials, outcomes, server.Status.Credentials, server.Generation)
	server.Status.ObservedGeneration = server.Generation
	if err := r.servers.UpdateStatus(ctx, server); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// degradedCondition rolls every credential's Pushed state into one condition
// that names the entries that failed (D9: loud, never fail-all).
func degradedCondition(
	spec []v1alpha1.AgentServerCredentialSpec,
	outcomes map[string]agentserver.CredentialOutcome,
) metav1.Condition {
	if len(spec) == 0 {
		return metav1.Condition{
			Type:    ConditionDegraded,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonNoCredentials,
			Message: "KapeAgentServer declares no credentials",
		}
	}
	var broken []string
	for _, cred := range spec {
		outcome := outcomes[cred.Name]
		if outcome.Pushed {
			continue
		}
		broken = append(broken, fmt.Sprintf("%s (%s)", cred.Name, outcome.Reason))
	}
	if len(broken) == 0 {
		return metav1.Condition{
			Type:    ConditionDegraded,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonCredentialsCurrent,
			Message: fmt.Sprintf("all %d credentials pushed to the OpenShell gateway", len(spec)),
		}
	}
	return metav1.Condition{
		Type:    ConditionDegraded,
		Status:  metav1.ConditionTrue,
		Reason:  ReasonCredentialsDegraded,
		Message: "credentials not pushed: " + strings.Join(broken, ", "),
	}
}

// credentialsStatus renders status.credentials[] in CR order: one Pushed
// condition per entry, plus the H6 fail-closed gap on the entries that are live.
// Transition timestamps are carried over so an unchanged status is a no-op write.
func credentialsStatus(
	spec []v1alpha1.AgentServerCredentialSpec,
	outcomes map[string]agentserver.CredentialOutcome,
	existing []v1alpha1.AgentServerCredentialStatus,
	generation int64,
) []v1alpha1.AgentServerCredentialStatus {
	previous := map[string][]metav1.Condition{}
	for _, status := range existing {
		previous[status.Name] = status.Conditions
	}
	statuses := make([]v1alpha1.AgentServerCredentialStatus, 0, len(spec))
	for _, cred := range spec {
		outcome := outcomes[cred.Name]
		pushed := metav1.Condition{
			Type:               ConditionPushed,
			Status:             metav1.ConditionTrue,
			Reason:             agentserver.Pushed,
			Message:            outcome.Message,
			ObservedGeneration: generation,
		}
		if !outcome.Pushed {
			pushed.Status = metav1.ConditionFalse
			pushed.Reason = outcome.Reason
		}
		conditions := setCondition(previous[cred.Name], pushed)
		if outcome.Pushed {
			// Static gap statement: added once, then carried as-is so a steady CR
			// does not rewrite status on every reconcile.
			if conditionByType(conditions, ConditionFailClosedEnforced) == nil {
				conditions = setCondition(conditions, metav1.Condition{
					Type:               ConditionFailClosedEnforced,
					Status:             metav1.ConditionFalse,
					Reason:             ReasonFailClosedUnavailable,
					Message:            failClosedMessage,
					ObservedGeneration: generation,
				})
			}
		} else {
			conditions = withoutCondition(conditions, ConditionFailClosedEnforced)
		}
		statuses = append(statuses, v1alpha1.AgentServerCredentialStatus{Name: cred.Name, Conditions: conditions})
	}
	return statuses
}

// conditionByType returns the condition with the given type, or nil.
func conditionByType(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// withoutCondition returns conditions minus the one with the given type.
func withoutCondition(conditions []metav1.Condition, condType string) []metav1.Condition {
	kept := make([]metav1.Condition, 0, len(conditions))
	for _, cond := range conditions {
		if cond.Type == condType {
			continue
		}
		kept = append(kept, cond)
	}
	return kept
}
