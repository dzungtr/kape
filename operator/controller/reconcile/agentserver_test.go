package reconcile_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	reconcile "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/infra/ports"
)

var agentServerKey = types.NamespacedName{Namespace: "kape-system", Name: "sre"}

func agentServerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		v1alpha1.AddToScheme,
		appsv1.AddToScheme,
		corev1.AddToScheme,
		rbacv1.AddToScheme,
		networkingv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			panic(err)
		}
	}
	return s
}

func newAgentServer() *v1alpha1.KapeAgentServer {
	return &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: "sre", Namespace: "kape-system", Generation: 3},
		Spec: v1alpha1.KapeAgentServerSpec{
			Description: "sre-triage: sandbox agents for the SRE alert pipeline",
			Controller:  v1alpha1.AgentServerControllerSpec{Image: "ghcr.io/kape-io/kape-controller:0.1.0"},
			Sandbox: v1alpha1.AgentServerSandboxSpec{
				Image:     "pi-image",
				Model:     "z-ai/glm-5.2",
				Resources: v1alpha1.SandboxResources{CPU: "1", Memory: "2Gi"},
			},
			ModelGateway: v1alpha1.AgentServerModelGatewaySpec{
				URL: "http://gw.aperture.svc.cluster.local/v1",
			},
		},
	}
}

func controllerWiring() domainconfig.ControllerWiring {
	return domainconfig.ControllerWiring{
		GatewayAddr:     "openshell.openshell.svc.cluster.local:8080",
		CredsDir:        "/var/run/kape-controller/creds",
		CredsSecret:     "kape-controller-creds",
		DefaultProvider: "openrouter",
		Transport:       "pull",
		EventStore:      "/tmp/controller-events.db",
	}
}

func newAgentServerReconciler(t *testing.T, wiring domainconfig.ControllerWiring, objs ...client.Object) (*reconcile.AgentServerReconciler, client.Client) {
	t.Helper()
	return newAgentServerReconcilerWith(t, wiring, nil, objs...)
}

// newAgentServerReconcilerWith builds the reconciler over a fake client, with an
// optional OpenShell writer (nil = no gateway configured).
func newAgentServerReconcilerWith(t *testing.T, wiring domainconfig.ControllerWiring, writer ports.OpenShellApplyPort, objs ...client.Object) (*reconcile.AgentServerReconciler, client.Client) {
	t.Helper()
	server := newAgentServer()
	objects := objs
	if !containsServer(objects) {
		objects = append([]client.Object{server}, objs...)
	}
	c := fake.NewClientBuilder().
		WithScheme(agentServerScheme()).
		WithObjects(objects...).
		WithStatusSubresource(server).
		Build()
	r := reconcile.NewAgentServerReconciler(
		k8sadapters.NewAgentServerRepository(c),
		k8sadapters.NewAgentServerAdapter(c),
		k8sadapters.NewSecretResolver(c),
		writer,
		wiring,
	)
	return r, c
}

// containsServer reports whether the fixtures already carry the KapeAgentServer,
// so a test can drive a CR with credentials without a duplicate-object panic.
func containsServer(objs []client.Object) bool {
	for _, obj := range objs {
		if _, ok := obj.(*v1alpha1.KapeAgentServer); ok {
			return true
		}
	}
	return false
}

func readyCondition(t *testing.T, c client.Client) *metav1.Condition {
	t.Helper()
	got, err := k8sadapters.NewAgentServerRepository(c).Get(context.Background(), agentServerKey)
	require.NoError(t, err)
	require.NotNil(t, got)
	return findCondition(got.Status.Conditions, "Ready")
}

// TestAgentServerReconciler_WiringIncomplete covers the operator-level wiring
// gate: an unset wiring var is reported on Ready instead of being defaulted,
// and nothing is materialised.
func TestAgentServerReconciler_WiringIncomplete(t *testing.T) {
	r, c := newAgentServerReconciler(t, domainconfig.ControllerWiring{})
	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	cond := readyCondition(t, c)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, reconcile.ReasonWiringIncomplete, cond.Reason)
	assert.Contains(t, cond.Message, "KAPE_GATEWAY_ADDR")
	assert.Contains(t, cond.Message, "KAPE_CREDS_SECRET")
	assert.Contains(t, cond.Message, "KAPE_EVENT_STORE")

	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep)
	assert.True(t, apierrors.IsNotFound(err), "no controller Deployment without complete wiring")
}

// TestAgentServerReconciler_AppliesAndReportsProgressing covers the happy path
// up to a not-yet-available Deployment: objects land in the CR's namespace, and
// Ready reflects the reconcile outcome.
func TestAgentServerReconciler_AppliesAndReportsProgressing(t *testing.T) {
	r, c := newAgentServerReconciler(t, controllerWiring())
	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep))
	var svc corev1.Service
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &svc))

	cond := readyCondition(t, c)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, reconcile.ReasonProgressing, cond.Reason)
	assert.Equal(t, int64(3), cond.ObservedGeneration)

	got, err := k8sadapters.NewAgentServerRepository(c).Get(context.Background(), agentServerKey)
	require.NoError(t, err)
	assert.Equal(t, int64(3), got.Status.ObservedGeneration)
}

// TestAgentServerReconciler_ReadyWhenAvailable covers the Ready=True outcome
// once the rendered controller is serving.
func TestAgentServerReconciler_ReadyWhenAvailable(t *testing.T) {
	r, c := newAgentServerReconciler(t, controllerWiring())
	ctx := context.Background()
	_, err := r.Reconcile(ctx, agentServerKey)
	require.NoError(t, err)

	depKey := types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}
	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, depKey, &dep))
	dep.Status.ObservedGeneration = dep.Generation
	dep.Status.AvailableReplicas = 1
	require.NoError(t, c.Status().Update(ctx, &dep))

	_, err = r.Reconcile(ctx, agentServerKey)
	require.NoError(t, err)
	cond := readyCondition(t, c)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, reconcile.ReasonDeployed, cond.Reason)
}

// TestAgentServerReconciler_OwnershipConflict covers the M5 invariant: a
// hand-wired object with the rendered name is reported, not adopted.
func TestAgentServerReconciler_OwnershipConflict(t *testing.T) {
	foreign := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kape-controller-sre", Namespace: "kape-system"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "legacy"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "legacy"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "legacy", Image: "legacy:latest"}}},
			},
		},
	}
	r, c := newAgentServerReconciler(t, controllerWiring(), foreign)
	_, err := r.Reconcile(context.Background(), agentServerKey)
	require.NoError(t, err)

	cond := readyCondition(t, c)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, reconcile.ReasonOwnershipConflict, cond.Reason)

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep))
	assert.Equal(t, "legacy:latest", dep.Spec.Template.Spec.Containers[0].Image)
}

// TestAgentServerReconciler_DeletedCRIsNoOp: a reconcile for a vanished CR
// returns cleanly (owner references clean up the rendered objects).
func TestAgentServerReconciler_DeletedCRIsNoOp(t *testing.T) {
	r, _ := newAgentServerReconciler(t, controllerWiring())
	res, err := r.Reconcile(context.Background(), types.NamespacedName{Namespace: "kape-system", Name: "gone"})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, res)
}

// TestControllerWiringMissingNames pins the reported var names.
func TestControllerWiringMissingNames(t *testing.T) {
	assert.Empty(t, controllerWiring().Missing())
	missing := domainconfig.ControllerWiring{Transport: "pull"}.Missing()
	assert.Equal(t, []string{
		"KAPE_GATEWAY_ADDR", "KAPE_CREDS_DIR", "KAPE_CREDS_SECRET", "KAPE_DEFAULT_PROVIDER", "KAPE_EVENT_STORE",
	}, missing)
}
