package ports

import (
	"context"

	"github.com/kape-io/kape/operator/domain/agentserver"
)

// CredentialSecretRef is one credentials[] entry reduced to what the resolver
// needs: the CR entry name and the same-namespace Secret holding its material.
// No namespace is carried — CEL rejects a cross-namespace secretRef at admission
// and the resolver reads only from the KapeAgentServer's own namespace (D10).
type CredentialSecretRef struct {
	Credential string
	Secret     string
}

// SecretResolver reads credential Secret material for the renderer (M4). The
// values it returns are in memory only: never logged, never persisted, never
// placed on the CR (ADR-0088).
type SecretResolver interface {
	// Resolve reads every ref from namespace, keyed by credentials[].name and
	// then credential key. Credentials that cannot be used come back as a
	// failure naming the reason — one broken Secret never blocks the rest (D9).
	Resolve(ctx context.Context, namespace string, refs []CredentialSecretRef) (agentserver.ResolvedSecrets, map[string]agentserver.CredentialFailure)
}

// OpenShellApplyPort converges the gateway-side provider records (M3). The
// caller holds no k8s state; this port is the only path credential values take
// out of the operator.
type OpenShellApplyPort interface {
	// Apply imports the provider profiles the records need, then creates or
	// updates each provider record, rotating a credential whose value drifted.
	// One outcome per record, in input order (D9). err is non-nil only when the
	// gateway itself is unreachable; per-credential failures ride the outcomes.
	Apply(ctx context.Context, records []agentserver.OpenShellRecord) ([]agentserver.CredentialOutcome, error)
}
