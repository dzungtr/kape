package controller_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/kape-io/kape/operator/controller"
	"github.com/kape-io/kape/operator/domain/agentserver"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

func newWatchScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func handlerWithLabel(name, ns, labelKey, labelVal string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{labelKey: labelVal},
		},
	}
}

func TestMapSkillToHandlers_EnqueuesLabelledHandlers(t *testing.T) {
	s := newWatchScheme()

	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	h1 := handlerWithLabel("handler-a", "kape-system", "kape.io/skill-ref-analyst", "true")
	h2 := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-b", Namespace: "kape-system"},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill, h1, h2).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	require.Len(t, requests, 1)
	assert.Equal(t, types.NamespacedName{Name: "handler-a", Namespace: "kape-system"}, requests[0].NamespacedName)
}

func TestMapSkillToHandlers_NoHandlers_ReturnsEmpty(t *testing.T) {
	s := newWatchScheme()
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	assert.Empty(t, requests)
}

func TestMapSkillToHandlers_WrongObjectType_ReturnsNil(t *testing.T) {
	s := newWatchScheme()
	notASkill := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-a", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(notASkill).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), notASkill)

	assert.Nil(t, requests)
}

func TestMapSkillToHandlers_MultipleHandlers_EnqueuesAll(t *testing.T) {
	s := newWatchScheme()
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	h1 := handlerWithLabel("handler-a", "kape-system", "kape.io/skill-ref-analyst", "true")
	h2 := handlerWithLabel("handler-b", "kape-system", "kape.io/skill-ref-analyst", "true")
	h3 := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-c", Namespace: "kape-system"},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill, h1, h2, h3).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	require.Len(t, requests, 2)
	names := []string{requests[0].NamespacedName.Name, requests[1].NamespacedName.Name}
	assert.Contains(t, names, "handler-a")
	assert.Contains(t, names, "handler-b")
}

// credentialSecretObj is a Secret carrying the operator's credential label.
func credentialSecretObj(name, namespace string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    map[string]string{agentserver.CredentialLabel: agentserver.CredentialLabelValue},
	}}
}

// agentServerWithSecret is a KapeAgentServer whose credentials[] entry names
// the given Secret.
func agentServerWithSecret(name, namespace, secretName string) *v1alpha1.KapeAgentServer {
	return &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.KapeAgentServerSpec{
			Credentials: []v1alpha1.AgentServerCredentialSpec{{
				Name:         "github",
				SecretRef:    v1alpha1.AgentServerSecretRef{Name: secretName},
				ProviderType: "git-https",
				HostPatterns: []string{"github.com:443"},
			}},
		},
	}
}

// TestMapSecretToAgentServers_EnqueuesReferencingCRs is the rotation loop: a
// labelled Secret edit re-enqueues the CRs that reference it.
func TestMapSecretToAgentServers_EnqueuesReferencingCRs(t *testing.T) {
	secret := credentialSecretObj("github-token", "kape-system")
	referencing := agentServerWithSecret("sre", "kape-system", "github-token")
	other := agentServerWithSecret("other", "kape-system", "other-token")
	foreignNamespace := agentServerWithSecret("sre", "other-namespace", "github-token")

	c := fake.NewClientBuilder().WithScheme(newWatchScheme()).
		WithObjects(secret, referencing, other, foreignNamespace).Build()

	requests := controller.MapSecretToAgentServers(c)(context.Background(), secret)

	require.Len(t, requests, 1, "only same-namespace CRs that reference the Secret")
	assert.Equal(t, types.NamespacedName{Name: "sre", Namespace: "kape-system"}, requests[0].NamespacedName)
}

// TestMapSecretToAgentServers_IgnoresUnlabelledSecret: Secrets outside the
// credential read path never trigger a reconcile (D10).
func TestMapSecretToAgentServers_IgnoresUnlabelledSecret(t *testing.T) {
	secret := credentialSecretObj("github-token", "kape-system")
	secret.Labels = nil
	referencing := agentServerWithSecret("sre", "kape-system", "github-token")

	c := fake.NewClientBuilder().WithScheme(newWatchScheme()).WithObjects(secret, referencing).Build()

	assert.Empty(t, controller.MapSecretToAgentServers(c)(context.Background(), secret))
}

// TestMapSecretToAgentServers_WrongObjectType: only Secrets map to CRs.
func TestMapSecretToAgentServers_WrongObjectType(t *testing.T) {
	server := agentServerWithSecret("sre", "kape-system", "github-token")
	c := fake.NewClientBuilder().WithScheme(newWatchScheme()).WithObjects(server).Build()

	assert.Nil(t, controller.MapSecretToAgentServers(c)(context.Background(), server))
}

// TestCredentialSecretPredicate pins the watch filter: only labelled Secrets
// enter the controller cache.
func TestCredentialSecretPredicate(t *testing.T) {
	p := controller.CredentialSecretPredicate()
	assert.True(t, p.Create(event.CreateEvent{Object: credentialSecretObj("github-token", "kape-system")}))
	assert.False(t, p.Create(event.CreateEvent{Object: &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: "sre", Namespace: "kape-system"},
	}}))
}
