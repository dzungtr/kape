//go:build envtest

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

// agentServerScheme is the scheme the KapeAgentServer envtests need: the CRD
// types plus every kind the reconciler renders.
func agentServerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(appsv1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(rbacv1.AddToScheme(s))
	utilruntime.Must(networkingv1.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

// startAgentServerEnv boots a throwaway API server with the repo's CRDs.
func startAgentServerEnv(t *testing.T) (*rest.Config, client.Client) {
	t.Helper()
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../../crds"},
		ErrorIfCRDPathMissing: true,
		Scheme:                agentServerScheme(),
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	c, err := client.New(cfg, client.Options{Scheme: agentServerScheme()})
	require.NoError(t, err)
	return cfg, c
}

// validAgentServer is a spec exercising the complete v0 schema: every spec
// group, description, and a credential registry entry.
func validAgentServer(name string) *v1alpha1.KapeAgentServer {
	return &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1alpha1.KapeAgentServerSpec{
			Description: "sre-triage: sandbox agents for the SRE alert pipeline",
			Controller: v1alpha1.AgentServerControllerSpec{
				Image:     "ghcr.io/kape-io/kape-controller:0.1.0",
				Resources: nil,
			},
			Sandbox: v1alpha1.AgentServerSandboxSpec{
				Image:     "ghcr.io/dzungtr/pi-openshell:openrouter",
				Model:     "z-ai/glm-5.2",
				Resources: v1alpha1.SandboxResources{CPU: "1", Memory: "2Gi"},
			},
			ModelGateway: v1alpha1.AgentServerModelGatewaySpec{
				URL: "http://model-gateway-http.aperture.svc.cluster.local/v1",
			},
			Credentials: []v1alpha1.AgentServerCredentialSpec{{
				Name:         "github",
				SecretRef:    v1alpha1.AgentServerSecretRef{Name: "github-token"},
				ProviderType: "git-https",
				HostPatterns: []string{"github.com:443", "api.github.com:443", "**.githubusercontent.com:443"},
			}},
			Network: &v1alpha1.AgentServerNetworkSpec{
				Ingress:          &v1alpha1.AgentServerIngressSpec{Host: "sre.kape.example.com", TLSSecretName: "sre-tls"},
				EgressPolicyRefs: []string{"controller-egress"},
			},
		},
	}
}

// TestKapeAgentServerCRDRoundTrip proves the complete v0 schema round-trips:
// all five spec groups, description, and the per-credential status schema are
// present now, so the credential slices need no CRD revision (spec #261 M1).
func TestKapeAgentServerCRDRoundTrip(t *testing.T) {
	_, c := startAgentServerEnv(t)
	ctx := context.Background()

	server := validAgentServer("round-trip")
	require.NoError(t, c.Create(ctx, server))

	var got v1alpha1.KapeAgentServer
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "round-trip"}, &got))
	assert.Equal(t, server.Spec.Description, got.Spec.Description)
	assert.Equal(t, server.Spec.Controller.Image, got.Spec.Controller.Image)
	assert.Equal(t, server.Spec.Sandbox, got.Spec.Sandbox)
	assert.Equal(t, server.Spec.ModelGateway, got.Spec.ModelGateway)
	require.Len(t, got.Spec.Credentials, 1)
	assert.Equal(t, "github-token", got.Spec.Credentials[0].SecretRef.Name)
	assert.Equal(t, []string{"github.com:443", "api.github.com:443", "**.githubusercontent.com:443"}, got.Spec.Credentials[0].HostPatterns)
	require.NotNil(t, got.Spec.Network)
	require.NotNil(t, got.Spec.Network.Ingress)
	assert.Equal(t, "sre.kape.example.com", got.Spec.Network.Ingress.Host)
	assert.Equal(t, "sre-tls", got.Spec.Network.Ingress.TLSSecretName)
	assert.Equal(t, []string{"controller-egress"}, got.Spec.Network.EgressPolicyRefs)

	// The per-credential status schema is written by the credential slice
	// (#265); it must accept a condition entry today.
	got.Status.Credentials = []v1alpha1.AgentServerCredentialStatus{{
		Name: "github",
		Conditions: []metav1.Condition{{
			Type:               "Pushed",
			Status:             metav1.ConditionTrue,
			Reason:             "ProviderCreated",
			LastTransitionTime: metav1.Now(),
		}},
	}}
	require.NoError(t, c.Status().Update(ctx, &got))
}

// TestKapeAgentServerCELRejections covers scenario 5: bad host patterns,
// duplicate credential names, and cross-namespace secretRefs are rejected at
// admission with a message naming the problem.
func TestKapeAgentServerCELRejections(t *testing.T) {
	_, c := startAgentServerEnv(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		mutate  func(*v1alpha1.KapeAgentServer)
		message string
	}{
		{
			name: "bad host pattern",
			mutate: func(s *v1alpha1.KapeAgentServer) {
				s.Spec.Credentials[0].HostPatterns = []string{"https://github.com/pulls"}
			},
			message: "hostPatterns entries must be host[:port] globs",
		},
		{
			name: "host pattern with path",
			mutate: func(s *v1alpha1.KapeAgentServer) {
				s.Spec.Credentials[0].HostPatterns = []string{"github.com:443/pulls"}
			},
			message: "hostPatterns entries must be host[:port] globs",
		},
		{
			name: "duplicate credential names",
			mutate: func(s *v1alpha1.KapeAgentServer) {
				s.Spec.Credentials = append(s.Spec.Credentials, s.Spec.Credentials[0])
			},
			message: "credentials[].name must be unique",
		},
		{
			name: "cross-namespace secretRef",
			mutate: func(s *v1alpha1.KapeAgentServer) {
				s.Spec.Credentials[0].SecretRef.Namespace = "openshell"
			},
			message: "secretRef must be same-namespace",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := validAgentServer("invalid-" + strings.ReplaceAll(tc.name, " ", "-"))
			tc.mutate(server)
			err := c.Create(ctx, server)
			require.Error(t, err, "case %d: expected admission rejection", i)
			assert.True(t, apierrors.IsInvalid(err), "want Invalid, got %v", err)
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

// TestAgentServerReconcileEnvtest covers scenarios 2 and 6 end-to-end against a
// real API server: a CR materialises the controller Deployment + Service +
// network resources in its namespace, status Ready reflects the outcome, and a
// deleted Deployment is recreated by the next reconcile.
func TestAgentServerReconcileEnvtest(t *testing.T) {
	cfg, c := startAgentServerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kape-agentserver"}}
	require.NoError(t, c.Create(ctx, ns))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 agentServerScheme(),
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	require.NoError(t, err)

	rec := reconcilehandler.NewAgentServerReconciler(
		k8sadapters.NewAgentServerRepository(mgr.GetClient()),
		k8sadapters.NewAgentServerAdapter(mgr.GetClient()),
		domainconfig.ControllerWiring{
			GatewayAddr:     "openshell.openshell.svc.cluster.local:8080",
			CredsDir:        "/var/run/kape-controller/creds",
			DefaultProvider: "openrouter",
			Transport:       "pull",
			EventStore:      "/var/lib/kape-controller/controller-events.db",
		},
	)
	require.NoError(t, kapecontroller.SetupAgentServerReconciler(mgr, rec, 1))
	go func() { _ = mgr.Start(ctx) }()

	server := validAgentServer("sre")
	server.Namespace = ns.Name
	require.NoError(t, c.Create(ctx, server))

	depKey := types.NamespacedName{Namespace: ns.Name, Name: "kape-controller-sre"}
	waitFor(t, "rendered controller Deployment", func() bool {
		var dep appsv1.Deployment
		return c.Get(ctx, depKey, &dep) == nil
	})

	var svc corev1.Service
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: "kape-controller-sre"}, &svc))
	var ing networkingv1.Ingress
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: "kape-controller-sre"}, &ing))
	assert.Equal(t, "sre.kape.example.com", ing.Spec.Rules[0].Host)

	// Scenario 6: deleting the controller Deployment converges back.
	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, depKey, &dep))
	require.NoError(t, c.Delete(ctx, &dep))
	waitFor(t, "recreated controller Deployment", func() bool {
		var again appsv1.Deployment
		return c.Get(ctx, depKey, &again) == nil
	})

	// Status Ready reflects the reconcile outcome: the fake-less API server has
	// no kubelet, so the Deployment never becomes available — Ready stays False
	// with a reason that names the state rather than reporting spurious success.
	waitFor(t, "Ready condition", func() bool {
		var got v1alpha1.KapeAgentServer
		if err := c.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: "sre"}, &got); err != nil {
			return false
		}
		for _, cond := range got.Status.Conditions {
			if cond.Type == "Ready" {
				return cond.Status == metav1.ConditionFalse && cond.Reason == reconcilehandler.ReasonProgressing
			}
		}
		return false
	})
}

// waitFor polls cond until it holds or the deadline expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
