package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	reconcile "github.com/kape-io/kape/operator/controller/reconcile"
	"github.com/kape-io/kape/operator/domain/agentserver"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/infra/ports"
)

// fakeWriter is a scripted OpenShellApplyPort: it reports the credentials it was
// handed as pushed (or applies its failure script) and records the records.
type fakeWriter struct {
	records  []agentserver.OpenShellRecord
	failures map[string]agentserver.CredentialFailure
	err      error
}

func (f *fakeWriter) Apply(_ context.Context, records []agentserver.OpenShellRecord) ([]agentserver.CredentialOutcome, error) {
	f.records = append(f.records, records...)
	outcomes := make([]agentserver.CredentialOutcome, 0, len(records))
	for _, rec := range records {
		if failure, failed := f.failures[rec.Credential]; failed {
			outcomes = append(outcomes, agentserver.FailedOutcome(rec.Credential, rec.Name, failure))
			continue
		}
		outcomes = append(outcomes, agentserver.PushedOutcome(rec))
	}
	return outcomes, f.err
}

// credentialServer is a CR with one git-https credential entry.
func credentialServer(credentials ...v1alpha1.AgentServerCredentialSpec) *v1alpha1.KapeAgentServer {
	server := newAgentServer()
	server.Spec.Credentials = credentials
	return server
}

func githubCredential(name, secret string) v1alpha1.AgentServerCredentialSpec {
	return v1alpha1.AgentServerCredentialSpec{
		Name:         name,
		SecretRef:    v1alpha1.AgentServerSecretRef{Name: secret},
		ProviderType: "git-https",
		HostPatterns: []string{"github.com:443", "api.github.com:443"},
	}
}

// labelledSecret is a credential Secret the resolver may read.
func labelledSecret(name string, data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kape-system",
			Labels:    map[string]string{agentserver.CredentialLabel: agentserver.CredentialLabelValue},
		},
		Data: map[string][]byte{},
	}
	for k, v := range data {
		secret.Data[k] = []byte(v)
	}
	return secret
}

// bindingsFromEnv decodes KAPE_CREDENTIAL_BINDINGS off the rendered Deployment.
func bindingsFromEnv(t *testing.T, c client.Client) []map[string]interface{} {
	t.Helper()
	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Namespace: agentServerKey.Namespace,
		Name:      agentserver.ControllerName(agentServerKey.Name),
	}, &dep))
	var raw string
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		if env.Name == agentserver.EnvCredentialBindings {
			raw = env.Value
		}
	}
	require.NotEmpty(t, raw, "KAPE_CREDENTIAL_BINDINGS is always rendered")
	var bindings []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &bindings))
	return bindings
}

func serverStatus(t *testing.T, c client.Client) *v1alpha1.KapeAgentServer {
	t.Helper()
	got, err := k8sadapters.NewAgentServerRepository(c).Get(context.Background(), agentServerKey)
	require.NoError(t, err)
	require.NotNil(t, got)
	return got
}

func credentialStatus(t *testing.T, server *v1alpha1.KapeAgentServer, name string) *v1alpha1.AgentServerCredentialStatus {
	t.Helper()
	for i := range server.Status.Credentials {
		if server.Status.Credentials[i].Name == name {
			return &server.Status.Credentials[i]
		}
	}
	return nil
}

// TestAgentServerReconciler_PushesLabelledSecret is scenario 1 at the reconcile
// level: a labelled Secret plus a credentials[] entry produces the provider
// record, the binding env, and Pushed=True.
func TestAgentServerReconciler_PushesLabelledSecret(t *testing.T) {
	writer := &fakeWriter{}
	server := credentialServer(githubCredential("github", "github-token"))
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer, server,
		labelledSecret("github-token", map[string]string{"token": "ghp_value"}))

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	require.Len(t, writer.records, 1)
	assert.Equal(t, "kape-kape-system-sre-github", writer.records[0].Name)
	assert.Equal(t, "git-https", writer.records[0].ProviderType)
	assert.Equal(t, "ghp_value", writer.records[0].Values["token"])

	bindings := bindingsFromEnv(t, c)
	require.Len(t, bindings, 1)
	assert.Equal(t, "kape-kape-system-sre-github", bindings[0]["provider"])
	assert.Equal(t, []interface{}{"github.com:443", "api.github.com:443"}, bindings[0]["hosts"])

	status := serverStatus(t, c)
	pushed := credentialStatus(t, status, "github")
	require.NotNil(t, pushed)
	cond := findCondition(pushed.Conditions, reconcile.ConditionPushed)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, agentserver.Pushed, cond.Reason)
	// H6: the gateway-side fail-closed gap is named per credential.
	gap := findCondition(pushed.Conditions, reconcile.ConditionFailClosedEnforced)
	require.NotNil(t, gap)
	assert.Equal(t, metav1.ConditionFalse, gap.Status)
	assert.Equal(t, reconcile.ReasonFailClosedUnavailable, gap.Reason)

	degraded := findCondition(status.Status.Conditions, reconcile.ConditionDegraded)
	require.NotNil(t, degraded)
	assert.Equal(t, metav1.ConditionFalse, degraded.Status)
	assert.Equal(t, reconcile.ReasonCredentialsCurrent, degraded.Reason)
}

// TestAgentServerReconciler_BrokenCredentialDegradesOnlyItself is scenario 3: a
// missing Secret is named on the CR, keeps its provider out of the binding env,
// and leaves the healthy credential working.
func TestAgentServerReconciler_BrokenCredentialDegradesOnlyItself(t *testing.T) {
	writer := &fakeWriter{}
	server := credentialServer(
		githubCredential("broken", "missing-token"),
		githubCredential("github", "github-token"),
	)
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer, server,
		labelledSecret("github-token", map[string]string{"token": "ghp_value"}))

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err, "a broken credential never fails the reconcile")

	require.Len(t, writer.records, 1)
	assert.Equal(t, "github", writer.records[0].Credential)

	bindings := bindingsFromEnv(t, c)
	require.Len(t, bindings, 1, "only the pushed provider is bound")
	assert.Equal(t, "kape-kape-system-sre-github", bindings[0]["provider"])

	// The controller Deployment still converges: degrade-loud, not fail-all.
	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Namespace: "kape-system", Name: "kape-controller-sre",
	}, &dep))

	status := serverStatus(t, c)
	broken := credentialStatus(t, status, "broken")
	require.NotNil(t, broken)
	cond := findCondition(broken.Conditions, reconcile.ConditionPushed)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, agentserver.ReasonSecretNotFound, cond.Reason)
	assert.Nil(t, findCondition(broken.Conditions, reconcile.ConditionFailClosedEnforced),
		"a credential that never pushed makes no fail-closed claim")

	healthy := credentialStatus(t, status, "github")
	require.NotNil(t, healthy)
	assert.Equal(t, metav1.ConditionTrue, findCondition(healthy.Conditions, reconcile.ConditionPushed).Status)

	degraded := findCondition(status.Status.Conditions, reconcile.ConditionDegraded)
	require.NotNil(t, degraded)
	assert.Equal(t, metav1.ConditionTrue, degraded.Status)
	assert.Equal(t, reconcile.ReasonCredentialsDegraded, degraded.Reason)
	assert.Contains(t, degraded.Message, "broken")
	assert.NotContains(t, degraded.Message, "github (", "the healthy credential is not named as degraded")
}

// TestAgentServerReconciler_UnlabelledSecretIsNotRead: label gating reaches
// status — an unlabelled Secret is reported, not silently used.
func TestAgentServerReconciler_UnlabelledSecretIsNotRead(t *testing.T) {
	unlabelled := labelledSecret("github-token", map[string]string{"token": "ghp_value"})
	unlabelled.Labels = nil
	writer := &fakeWriter{}
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer,
		credentialServer(githubCredential("github", "github-token")), unlabelled)

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	assert.Empty(t, writer.records)
	assert.Empty(t, bindingsFromEnv(t, c))

	cond := findCondition(credentialStatus(t, serverStatus(t, c), "github").Conditions, reconcile.ConditionPushed)
	require.NotNil(t, cond)
	assert.Equal(t, agentserver.ReasonSecretNotLabeled, cond.Reason)
}

// TestAgentServerReconciler_RejectsCrossNamespaceSecretRef: a secretRef carrying
// a namespace is refused before any read (defence in depth behind CEL), and the
// credential is named on status.
func TestAgentServerReconciler_RejectsCrossNamespaceSecretRef(t *testing.T) {
	credential := githubCredential("github", "github-token")
	credential.SecretRef.Namespace = "openshell"
	writer := &fakeWriter{}
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer,
		credentialServer(credential),
		labelledSecret("github-token", map[string]string{"token": "ghp_value"}))

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	assert.Empty(t, writer.records)
	assert.Empty(t, bindingsFromEnv(t, c), "a cross-namespace reference never binds an endpoint")

	cond := findCondition(credentialStatus(t, serverStatus(t, c), "github").Conditions, reconcile.ConditionPushed)
	require.NotNil(t, cond)
	assert.Equal(t, agentserver.ReasonCrossNamespaceSecretRef, cond.Reason)
}

// TestAgentServerReconciler_WriterFailureDegradesAndRequeues: a gateway rejection
// names the credential in the binding env-free state; an unreachable gateway
// surfaces an error so the reconcile is retried, with the Deployment still
// converged.
func TestAgentServerReconciler_WriterFailureDegradesAndRequeues(t *testing.T) {
	gatewayErr := errors.New("gateway unavailable")
	writer := &fakeWriter{
		failures: map[string]agentserver.CredentialFailure{
			"github": {Reason: agentserver.ReasonGatewayUnavailable, Message: "OpenShell gateway unreachable: connection refused"},
		},
		err: gatewayErr,
	}
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer,
		credentialServer(githubCredential("github", "github-token")),
		labelledSecret("github-token", map[string]string{"token": "ghp_value"}))

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.ErrorIs(t, err, gatewayErr, "a transport failure is retried")

	assert.Empty(t, bindingsFromEnv(t, c), "an unpushed provider is never bound")
	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Namespace: "kape-system", Name: "kape-controller-sre",
	}, &dep), "the controller still converges")

	degraded := findCondition(serverStatus(t, c).Status.Conditions, reconcile.ConditionDegraded)
	require.NotNil(t, degraded)
	assert.Equal(t, metav1.ConditionTrue, degraded.Status)
	assert.Contains(t, degraded.Message, "github")
}

// TestAgentServerReconciler_NoGatewayConfigured: without a gateway every
// credential is named, never silently unprovisioned.
func TestAgentServerReconciler_NoGatewayConfigured(t *testing.T) {
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), nil,
		credentialServer(githubCredential("github", "github-token")),
		labelledSecret("github-token", map[string]string{"token": "ghp_value"}))

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	cond := findCondition(credentialStatus(t, serverStatus(t, c), "github").Conditions, reconcile.ConditionPushed)
	require.NotNil(t, cond)
	assert.Equal(t, agentserver.ReasonGatewayNotConfigured, cond.Reason)
}

// TestAgentServerReconciler_NoCredentialsKeepsBindingsEmpty: a CR without
// credentials is unchanged from #263's contract — bindings [], no per-credential
// status, Degraded=False/NoCredentials.
func TestAgentServerReconciler_NoCredentialsKeepsBindingsEmpty(t *testing.T) {
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), &fakeWriter{})

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	assert.Empty(t, bindingsFromEnv(t, c))
	status := serverStatus(t, c)
	assert.Empty(t, status.Status.Credentials)
	degraded := findCondition(status.Status.Conditions, reconcile.ConditionDegraded)
	require.NotNil(t, degraded)
	assert.Equal(t, reconcile.ReasonNoCredentials, degraded.Reason)
}

// TestAgentServerReconciler_CredentialsStayOffTheCR pins the D7 invariant at the
// reconcile boundary: no credential value reaches the CR object or its status.
func TestAgentServerReconciler_CredentialsStayOffTheCR(t *testing.T) {
	writer := &fakeWriter{}
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer,
		credentialServer(githubCredential("github", "github-token")),
		labelledSecret("github-token", map[string]string{"token": "ghp_super_secret"}))

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	status := serverStatus(t, c)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "ghp_super_secret")

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Namespace: "kape-system", Name: "kape-controller-sre",
	}, &dep))
	deployment, err := json.Marshal(dep)
	require.NoError(t, err)
	assert.NotContains(t, string(deployment), "ghp_super_secret", "secret material never reaches the Deployment")
}

// TestAgentServerReconciler_SecretEditRepushes is scenario 2 at the reconcile
// level: after the Secret value changes, the writer receives the new value.
func TestAgentServerReconciler_SecretEditRepushes(t *testing.T) {
	writer := &fakeWriter{}
	channel := labelledSecret("github-token", map[string]string{"token": "ghp_value_old"})
	r, c := newAgentServerReconcilerWith(t, controllerWiring(), writer,
		credentialServer(githubCredential("github", "github-token")), channel)

	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)
	require.Len(t, writer.records, 1)
	assert.Equal(t, "ghp_value_old", writer.records[0].Values["token"])

	var stored corev1.Secret
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: "github-token"}, &stored))
	stored.Data["token"] = []byte("ghp_value_new")
	require.NoError(t, c.Update(context.Background(), &stored))

	_, err = r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)
	require.Len(t, writer.records, 2)
	assert.Equal(t, "ghp_value_new", writer.records[1].Values["token"])
}

var _ ports.OpenShellApplyPort = (*fakeWriter)(nil)
