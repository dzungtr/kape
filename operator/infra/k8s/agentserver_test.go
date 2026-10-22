package k8s_test

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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kape-io/kape/operator/domain/agentserver"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/infra/ports"
)

func agentServerScheme() *runtime.Scheme {
	s := newTestScheme()
	_ = rbacv1.AddToScheme(s)
	_ = networkingv1.AddToScheme(s)
	return s
}

func agentServer() *v1alpha1.KapeAgentServer {
	return &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: "sre", Namespace: "kape-system", UID: "uid-as", Generation: 1},
		Spec: v1alpha1.KapeAgentServerSpec{
			Description: "sre-triage",
			Controller: v1alpha1.AgentServerControllerSpec{
				Image: "ghcr.io/kape-io/kape-controller:0.1.0",
			},
			Sandbox: v1alpha1.AgentServerSandboxSpec{
				Image:     "pi-image",
				Model:     "z-ai/glm-5.2",
				Resources: v1alpha1.SandboxResources{CPU: "1", Memory: "2Gi"},
			},
			ModelGateway: v1alpha1.AgentServerModelGatewaySpec{URL: "http://gw.aperture.svc.cluster.local/v1"},
		},
	}
}

func testWiring() domainconfig.ControllerWiring {
	return domainconfig.ControllerWiring{
		GatewayAddr:     "openshell.openshell.svc.cluster.local:8080",
		CredsDir:        "/var/run/kape-controller/creds",
		CredsSecret:     "kape-controller-creds",
		DefaultProvider: "openrouter",
		Transport:       "pull",
		EventStore:      "/var/lib/kape-controller/controller-events.db",
		ExtraEnv: []corev1.EnvVar{
			{Name: "KAPE_MONITOR_INTERVAL_SECONDS", Value: "60"},
			// A wiring entry that would shadow the CR contract is dropped.
			{Name: "KAPE_DEFAULT_IMAGE", Value: "shadowed-image"},
		},
	}
}

func newAgentServerAdapter(objs ...client.Object) (*k8sadapters.AgentServerAdapter, client.Client) {
	c := fake.NewClientBuilder().WithScheme(agentServerScheme()).WithObjects(objs...).Build()
	return k8sadapters.NewAgentServerAdapter(c), c
}

// TestAgentServerAdapterApplyCreatesRenderedObjects covers M5: the controller
// Deployment, its ServiceAccount + RoleBinding, and the Service land in the
// CR's namespace with the CR-derived env contract and the wiring env.
func TestAgentServerAdapterApplyCreatesRenderedObjects(t *testing.T) {
	server := agentServer()
	a, c := newAgentServerAdapter()
	ctx := context.Background()

	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep))
	assert.Equal(t, "ghcr.io/kape-io/kape-controller:0.1.0", dep.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, "kape-controller-sre", dep.Spec.Template.Spec.ServiceAccountName)
	require.Len(t, dep.Spec.Template.Spec.Containers, 1)
	env := map[string]string{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	// Contract env (renderer) + cluster wiring (operator), all present exactly once.
	assert.Equal(t, "pi-image", env["KAPE_DEFAULT_IMAGE"])
	assert.Equal(t, "z-ai/glm-5.2", env["KAPE_DEFAULT_MODEL"])
	assert.Equal(t, `{"cpu":"1","memory":"2Gi"}`, env["KAPE_DEFAULT_RESOURCES"])
	assert.Equal(t, "http://gw.aperture.svc.cluster.local/v1", env["KAPE_MODEL_GATEWAY_URL"])
	assert.Equal(t, "sre-triage", env["KAPE_SERVER_DESCRIPTION"])
	assert.Equal(t, "[]", env["KAPE_CREDENTIAL_BINDINGS"])
	assert.Equal(t, "openshell.openshell.svc.cluster.local:8080", env["KAPE_GATEWAY_ADDR"])
	assert.Equal(t, "openrouter", env["KAPE_DEFAULT_PROVIDER"])
	assert.Equal(t, "pull", env["KAPE_TRANSPORT"])
	assert.Equal(t, "pi-image", env["KAPE_DEFAULT_IMAGE"], "wiring must never shadow the CR contract")
	assert.Equal(t, "60", env["KAPE_MONITOR_INTERVAL_SECONDS"], "chart passthrough env reaches the controller")
	assert.Len(t, dep.Spec.Template.Spec.Containers[0].Env, 12)
	assert.Equal(t, "sre", dep.Labels[agentserver.AgentServerLabel])

	var sa corev1.ServiceAccount
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &sa))

	var rb rbacv1.RoleBinding
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &rb))
	assert.Equal(t, k8sadapters.SandboxReaderClusterRole, rb.RoleRef.Name)
	require.Len(t, rb.Subjects, 1)
	assert.Equal(t, "kape-controller-sre", rb.Subjects[0].Name)

	var svc corev1.Service
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &svc))
	assert.Equal(t, agentserver.SelectorLabels("sre"), svc.Spec.Selector)

	// Wiring mounts: the mTLS Secret at CredsDir, an emptyDir for the event log.
	mounts := map[string]corev1.VolumeMount{}
	for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		mounts[m.Name] = m
	}
	require.Contains(t, mounts, "creds")
	assert.Equal(t, "/var/run/kape-controller/creds", mounts["creds"].MountPath)
	assert.True(t, mounts["creds"].ReadOnly)
	require.Contains(t, mounts, "event-store")
	assert.Equal(t, "/var/lib/kape-controller", mounts["event-store"].MountPath)

	secrets := map[string]string{}
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Secret != nil {
			secrets[v.Name] = v.Secret.SecretName
		}
	}
	assert.Equal(t, "kape-controller-creds", secrets["creds"])

	// No Ingress unless the CR declares one.
	var ing networkingv1.Ingress
	err := c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &ing)
	assert.True(t, apierrors.IsNotFound(err))
}

// TestAgentServerAdapterApplyIsConvergent: re-applying the same DesiredState
// changes nothing and keeps exactly one object per kind.
func TestAgentServerAdapterApplyIsConvergent(t *testing.T) {
	server := agentServer()
	a, c := newAgentServerAdapter()
	ctx := context.Background()
	desired := agentserver.Render(server, nil)

	require.NoError(t, a.Apply(ctx, server, desired, testWiring()))
	var first appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &first))

	require.NoError(t, a.Apply(ctx, server, desired, testWiring()))
	var second appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &second))

	assert.Equal(t, first.ResourceVersion, second.ResourceVersion, "an unchanged re-apply must not patch")
	assert.Equal(t, first.Spec, second.Spec)
}

// TestAgentServerAdapterApplyUpdatesSpecOnChange: editing the CR converges the
// Deployment env in place.
// TestAgentServerAdapterApplyIgnoresAPIDefaults pins the fix for the reconcile
// hot loop: the API server fills defaulted fields the renderer never sets
// (terminationMessagePath, dnsPolicy, probe thresholds, volume defaultMode, …).
// A raw merge-patch diff is therefore never empty against a live object, and
// patching on it bumped metadata.generation on every reconcile — the rendered
// controller Deployment never reported availability. An existing object that
// already satisfies every rendered field must not be patched.
func TestAgentServerAdapterApplyIgnoresAPIDefaults(t *testing.T) {
	server := agentServer()
	a, c := newAgentServerAdapter()
	ctx := context.Background()
	desired := agentserver.Render(server, nil)

	require.NoError(t, a.Apply(ctx, server, desired, testWiring()))

	// Simulate the API server's defaulting + status write on the live object.
	var live appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &live))
	live.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirst
	live.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
	live.Spec.Template.Spec.Containers[0].TerminationMessagePath = "/dev/termination-log"
	live.Spec.Template.Spec.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	live.Spec.Template.Spec.Containers[0].ReadinessProbe.SuccessThreshold = 1
	live.Spec.Template.Spec.Containers[0].ReadinessProbe.TimeoutSeconds = 1
	live.Spec.Template.Spec.Volumes[0].Secret.DefaultMode = func() *int32 { m := int32(420); return &m }()
	require.NoError(t, c.Update(ctx, &live))

	var defaulted appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &defaulted))

	require.NoError(t, a.Apply(ctx, server, desired, testWiring()))
	var after appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &after))

	// The patch must not fire: a patched Deployment bumps metadata.generation,
	// which resets the observedGeneration/availableReplicas bookkeeping the
	// Ready condition reads, and the Deployment never reports available.
	assert.Equal(t, defaulted.ResourceVersion, after.ResourceVersion, "API defaults alone must not trigger a patch")
	assert.Equal(t, defaulted.Generation, after.Generation, "a no-op reconcile must not bump generation")
}

func TestAgentServerAdapterApplyUpdatesSpecOnChange(t *testing.T) {
	server := agentServer()
	a, c := newAgentServerAdapter()
	ctx := context.Background()
	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))

	server.Spec.Sandbox.Model = "other/model"
	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep))
	env := map[string]string{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	assert.Equal(t, "other/model", env["KAPE_DEFAULT_MODEL"])
}

// TestAgentServerAdapterNeverAdopts: an object without the operator's
// ownership labels is reported, never patched (M5 invariant).
func TestAgentServerAdapterNeverAdopts(t *testing.T) {
	server := agentServer()
	foreign := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kape-controller-sre", Namespace: "kape-system"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "someone-else"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "someone-else"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "hand-wired", Image: "legacy:latest"}}},
			},
		},
	}
	a, c := newAgentServerAdapter(foreign)
	ctx := context.Background()

	err := a.Apply(ctx, server, agentserver.Render(server, nil), testWiring())
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotOwned)

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep))
	assert.Equal(t, "legacy:latest", dep.Spec.Template.Spec.Containers[0].Image, "foreign object must be untouched")
}

// TestAgentServerAdapterPrunesUndeclaredIngress: dropping spec.network.ingress
// removes the rendered Ingress but never a foreign one.
func TestAgentServerAdapterPrunesUndeclaredIngress(t *testing.T) {
	server := agentServer()
	server.Spec.Network = &v1alpha1.AgentServerNetworkSpec{
		Ingress: &v1alpha1.AgentServerIngressSpec{Host: "sre.kape.example.com"},
	}
	a, c := newAgentServerAdapter()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}

	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))
	var ing networkingv1.Ingress
	require.NoError(t, c.Get(ctx, key, &ing))

	server.Spec.Network = nil
	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))
	err := c.Get(ctx, key, &ing)
	assert.True(t, apierrors.IsNotFound(err), "rendered Ingress must be pruned")

	foreign := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "kape-controller-sre", Namespace: "kape-system"}}
	a2, c2 := newAgentServerAdapter(foreign)
	require.NoError(t, a2.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))
	require.NoError(t, c2.Get(ctx, key, &ing), "a foreign Ingress is not ours to delete")
}

// TestAgentServerAdapterDeploymentStatus covers the Ready signal the reconciler
// reads.
func TestAgentServerAdapterDeploymentStatus(t *testing.T) {
	server := agentServer()
	a, c := newAgentServerAdapter()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}

	_, found, err := a.DeploymentStatus(ctx, key)
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), testWiring()))
	available, found, err := a.DeploymentStatus(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	assert.False(t, available, "a freshly created Deployment is not available yet")

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, key, &dep))
	dep.Status.ObservedGeneration = dep.Generation
	dep.Status.AvailableReplicas = 1
	require.NoError(t, c.Status().Update(ctx, &dep))

	available, found, err = a.DeploymentStatus(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, available)
}

// TestControllerEnvContractIsNeverShadowed pins the guard the chart's
// passthrough lists rely on: wiring env (fixed or passthrough) can never
// replace a CR-derived contract var.
func TestControllerEnvContractIsNeverShadowed(t *testing.T) {
	server := agentServer()
	wiring := testWiring()
	wiring.ExtraEnv = []corev1.EnvVar{
		{Name: "KAPE_DEFAULT_IMAGE", Value: "shadow-1"},
		{Name: "KAPE_DEFAULT_MODEL", Value: "shadow-2"},
		{Name: "KAPE_DEFAULT_RESOURCES", Value: "shadow-3"},
		{Name: "KAPE_MODEL_GATEWAY_URL", Value: "shadow-4"},
		{Name: "KAPE_SERVER_DESCRIPTION", Value: "shadow-5"},
		{Name: "KAPE_CREDENTIAL_BINDINGS", Value: "shadow-6"},
		{Name: "KAPE_REVIVE_MAX", Value: "5"},
	}
	a, c := newAgentServerAdapter()
	ctx := context.Background()
	require.NoError(t, a.Apply(ctx, server, agentserver.Render(server, nil), wiring))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "kape-system", Name: "kape-controller-sre"}, &dep))
	env := map[string]string{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		require.NotContains(t, env, e.Name, "duplicate env name %s", e.Name)
		env[e.Name] = e.Value
	}
	assert.Equal(t, "pi-image", env["KAPE_DEFAULT_IMAGE"])
	assert.Equal(t, "z-ai/glm-5.2", env["KAPE_DEFAULT_MODEL"])
	assert.Equal(t, `{"cpu":"1","memory":"2Gi"}`, env["KAPE_DEFAULT_RESOURCES"])
	assert.Equal(t, "http://gw.aperture.svc.cluster.local/v1", env["KAPE_MODEL_GATEWAY_URL"])
	assert.Equal(t, "sre-triage", env["KAPE_SERVER_DESCRIPTION"])
	assert.Equal(t, "[]", env["KAPE_CREDENTIAL_BINDINGS"])
	assert.Equal(t, "5", env["KAPE_REVIVE_MAX"], "non-contract passthrough entries still ride along")
}
