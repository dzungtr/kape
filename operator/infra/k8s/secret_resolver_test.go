package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kape-io/kape/operator/domain/agentserver"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/infra/ports"
)

func resolverScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

// credentialSecret is a Secret carrying the operator's credential label.
func credentialSecret(name, namespace string, data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{agentserver.CredentialLabel: agentserver.CredentialLabelValue},
		},
		Data: map[string][]byte{},
	}
	for k, v := range data {
		secret.Data[k] = []byte(v)
	}
	return secret
}

func newResolver(objs ...client.Object) *k8sadapters.SecretResolver {
	return k8sadapters.NewSecretResolver(
		fake.NewClientBuilder().WithScheme(resolverScheme()).WithObjects(objs...).Build())
}

// TestSecretResolver_ReadsLabelledSameNamespaceSecret is the happy path: a
// labelled Secret in the CR's namespace resolves to its data keys.
func TestSecretResolver_ReadsLabelledSameNamespaceSecret(t *testing.T) {
	r := newResolver(credentialSecret("github-token", "kape-system", map[string]string{"token": "ghp_value"}))

	resolved, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "github", Secret: "github-token"},
	})

	assert.Empty(t, failures)
	assert.Equal(t, agentserver.ResolvedSecrets{"github": {"token": "ghp_value"}}, resolved)
}

// TestSecretResolver_LabelGating: an unlabelled Secret is never treated as
// credential material (ADR-0088 — reads are label-gated).
func TestSecretResolver_LabelGating(t *testing.T) {
	unlabelled := credentialSecret("github-token", "kape-system", map[string]string{"token": "ghp_value"})
	unlabelled.Labels = nil
	r := newResolver(unlabelled)

	resolved, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "github", Secret: "github-token"},
	})

	assert.Empty(t, resolved)
	require.Contains(t, failures, "github")
	assert.Equal(t, agentserver.ReasonSecretNotLabeled, failures["github"].Reason)
	assert.NotContains(t, failures["github"].Message, "ghp_value")
}

// TestSecretResolver_LabelGatingRequiresExactValue: `kape.io/credential: "false"`
// is not a credential Secret.
func TestSecretResolver_LabelGatingRequiresExactValue(t *testing.T) {
	falsey := credentialSecret("github-token", "kape-system", map[string]string{"token": "ghp_value"})
	falsey.Labels[agentserver.CredentialLabel] = "false"
	r := newResolver(falsey)

	_, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "github", Secret: "github-token"},
	})
	assert.Equal(t, agentserver.ReasonSecretNotLabeled, failures["github"].Reason)
}

// TestSecretResolver_SameNamespaceOnly: a Secret of the same name in another
// namespace is invisible — the resolver reads the CR's namespace only (D10).
func TestSecretResolver_SameNamespaceOnly(t *testing.T) {
	r := newResolver(credentialSecret("github-token", "other-namespace", map[string]string{"token": "other_value"}))

	resolved, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "github", Secret: "github-token"},
	})

	assert.Empty(t, resolved)
	require.Contains(t, failures, "github")
	assert.Equal(t, agentserver.ReasonSecretNotFound, failures["github"].Reason)
	assert.NotContains(t, failures["github"].Message, "other_value")
}

// TestSecretResolver_MissingSecretIsANamedFailure: a missing Secret is reported
// for that credential and does not stop the others (D9).
func TestSecretResolver_MissingSecretIsANamedFailure(t *testing.T) {
	r := newResolver(credentialSecret("github-token", "kape-system", map[string]string{"token": "ghp_value"}))

	resolved, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "broken", Secret: "missing-token"},
		{Credential: "github", Secret: "github-token"},
	})

	assert.Contains(t, failures, "broken")
	assert.Equal(t, agentserver.ReasonSecretNotFound, failures["broken"].Reason)
	assert.Contains(t, failures["broken"].Message, "kape-system/missing-token")
	assert.Equal(t, agentserver.ResolvedSecrets{"github": {"token": "ghp_value"}}, resolved)
}

// TestSecretResolver_EmptySecretIsANamedFailure: a Secret with no keys cannot
// satisfy a provider profile's required credential.
func TestSecretResolver_EmptySecretIsANamedFailure(t *testing.T) {
	r := newResolver(credentialSecret("github-token", "kape-system", nil))

	_, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "github", Secret: "github-token"},
	})

	assert.Equal(t, agentserver.ReasonSecretNoData, failures["github"].Reason)
}

// TestSecretResolver_MissingRefName: an entry without a secretRef name (only
// reachable when admission is bypassed) is reported, never read as "".
func TestSecretResolver_MissingRefName(t *testing.T) {
	r := newResolver()

	_, failures := r.Resolve(context.Background(), "kape-system", []ports.CredentialSecretRef{
		{Credential: "github"},
	})

	assert.Equal(t, agentserver.ReasonSecretNotFound, failures["github"].Reason)
}

// TestSecretResolver_NoRefs: nothing to resolve, nothing read.
func TestSecretResolver_NoRefs(t *testing.T) {
	r := newResolver()

	resolved, failures := r.Resolve(context.Background(), "kape-system", nil)
	assert.Empty(t, resolved)
	assert.Empty(t, failures)
}
