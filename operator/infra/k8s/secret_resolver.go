package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kape-io/kape/operator/domain/agentserver"
	"github.com/kape-io/kape/operator/infra/ports"
)

// SecretResolver reads credential Secret material for the renderer (M4,
// ADR-0088). It is the operator's only secret-read path: same namespace, label
// gated (kape.io/credential=true), get-only RBAC, values in memory only.
//
// Nothing here logs or returns a value in an error: failures name the credential
// and the Secret, never the data (D7 invariant).
type SecretResolver struct {
	c client.Client
}

// NewSecretResolver creates a SecretResolver over the operator's client.
func NewSecretResolver(c client.Client) *SecretResolver {
	return &SecretResolver{c: c}
}

// Resolve reads each ref from namespace and returns the material keyed by
// credentials[].name. A credential is reported as failed — never silently
// dropped — when its Secret is missing, unlabelled, empty, or unreadable (D9).
func (r *SecretResolver) Resolve(
	ctx context.Context,
	namespace string,
	refs []ports.CredentialSecretRef,
) (agentserver.ResolvedSecrets, map[string]agentserver.CredentialFailure) {
	resolved := agentserver.ResolvedSecrets{}
	failures := map[string]agentserver.CredentialFailure{}
	for _, ref := range refs {
		values, failure := r.resolveOne(ctx, namespace, ref)
		if failure != nil {
			failures[ref.Credential] = *failure
			continue
		}
		resolved[ref.Credential] = values
	}
	return resolved, failures
}

func (r *SecretResolver) resolveOne(
	ctx context.Context,
	namespace string,
	ref ports.CredentialSecretRef,
) (map[string]string, *agentserver.CredentialFailure) {
	if ref.Secret == "" {
		// Defence in depth: CEL requires a secretRef name at admission, so an
		// empty name means the object bypassed admission.
		return nil, failure(agentserver.ReasonSecretNotFound,
			fmt.Sprintf("credential %q declares no secretRef name", ref.Credential))
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: namespace, Name: ref.Secret}
	if err := r.c.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, failure(agentserver.ReasonSecretNotFound,
				fmt.Sprintf("credential %q references Secret %s/%s, which does not exist", ref.Credential, namespace, ref.Secret))
		}
		return nil, failure(agentserver.ReasonSecretReadFailed,
			fmt.Sprintf("credential %q: reading Secret %s/%s: %s", ref.Credential, namespace, ref.Secret, err.Error()))
	}
	if secret.Labels[agentserver.CredentialLabel] != agentserver.CredentialLabelValue {
		return nil, failure(agentserver.ReasonSecretNotLabeled,
			fmt.Sprintf("Secret %s/%s is not labelled %s=%s; the operator reads only credential Secrets",
				namespace, ref.Secret, agentserver.CredentialLabel, agentserver.CredentialLabelValue))
	}
	if len(secret.Data) == 0 {
		return nil, failure(agentserver.ReasonSecretNoData,
			fmt.Sprintf("Secret %s/%s carries no data keys", namespace, ref.Secret))
	}
	values := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		values[k] = string(v)
	}
	return values, nil
}

func failure(reason, message string) *agentserver.CredentialFailure {
	return &agentserver.CredentialFailure{Reason: reason, Message: message}
}
