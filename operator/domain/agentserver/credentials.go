package agentserver

// Reasons reported on a credential's Pushed condition. They are a stable
// vocabulary: the resolver and the OpenShell writer both speak it, and the
// reconciler copies it verbatim onto status.credentials[] (D9 — a broken
// credential must be loud about *why*, never silently missing).
const (
	// Pushed is the success reason: the provider record exists on the gateway
	// with the current Secret values.
	Pushed = "Pushed"

	// Resolver failures — the credential Secret could not be read.
	ReasonSecretNotFound          = "SecretNotFound"
	ReasonSecretNotLabeled        = "SecretNotLabeled"
	ReasonSecretNoData            = "SecretNoData"
	ReasonSecretReadFailed        = "SecretReadFailed"
	ReasonCrossNamespaceSecretRef = "CrossNamespaceSecretRef"

	// Writer failures — the gateway-side record could not be converged.
	ReasonGatewayNotConfigured = "GatewayNotConfigured"
	ReasonGatewayUnavailable   = "GatewayUnavailable"
	ReasonGatewayRejected      = "GatewayRejected"
	ReasonUnknownProviderType  = "UnknownProviderType"
	ReasonMissingCredentialKey = "MissingCredentialKey"
	ReasonProfileLintFailed    = "ProfileLintFailed"
)

// CredentialFailure explains why one credential could not be used. Message is
// human-readable and must never contain secret material.
type CredentialFailure struct {
	Reason  string
	Message string
}

// CredentialOutcome is one credential's provisioning result for a reconcile:
// pushed to the gateway, or failed with a named reason. Provider is the OpenShell
// record name (H2), carried so status messages can name both the CR entry and the
// gateway record.
type CredentialOutcome struct {
	Credential string
	Provider   string
	Pushed     bool
	Reason     string
	Message    string
}

// PushedOutcome reports a credential whose provider record is current.
func PushedOutcome(record OpenShellRecord) CredentialOutcome {
	return CredentialOutcome{
		Credential: record.Credential,
		Provider:   record.Name,
		Pushed:     true,
		Reason:     Pushed,
		Message:    "provider record current on the OpenShell gateway",
	}
}

// FailedOutcome reports a credential that could not be brought up to date.
func FailedOutcome(credential, provider string, failure CredentialFailure) CredentialOutcome {
	return CredentialOutcome{
		Credential: credential,
		Provider:   provider,
		Reason:     failure.Reason,
		Message:    failure.Message,
	}
}
