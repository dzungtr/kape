package agentserver_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kape-io/kape/operator/domain/agentserver"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// testServer is a CR that passed API validation, with a fixed UID so the
// rendered owner reference is stable across runs.
func testServer() *v1alpha1.KapeAgentServer {
	return &v1alpha1.KapeAgentServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sre",
			Namespace: "kape-system",
			UID:       types.UID("11111111-2222-3333-4444-555555555555"),
		},
		Spec: v1alpha1.KapeAgentServerSpec{
			Description: "sre-triage: sandbox agents for the SRE alert pipeline",
			Controller: v1alpha1.AgentServerControllerSpec{
				Image: "ghcr.io/kape-io/kape-controller:0.1.0",
			},
			Sandbox: v1alpha1.AgentServerSandboxSpec{
				Image:     "ghcr.io/dzungtr/pi-openshell:openrouter",
				Model:     "z-ai/glm-5.2",
				Resources: v1alpha1.SandboxResources{CPU: "1", Memory: "2Gi"},
			},
			ModelGateway: v1alpha1.AgentServerModelGatewaySpec{
				URL: "http://model-gateway-http.aperture.svc.cluster.local/v1",
			},
		},
	}
}

// TestRenderGolden pins the complete DesiredState of representative
// no-credential specs: same CR in, same objects out (M2 invariant).
func TestRenderGolden(t *testing.T) {
	cases := map[string]*v1alpha1.KapeAgentServer{
		"minimal": testServer(),
		"ingress": func() *v1alpha1.KapeAgentServer {
			s := testServer()
			s.Spec.Network = &v1alpha1.AgentServerNetworkSpec{
				Ingress:          &v1alpha1.AgentServerIngressSpec{Host: "sre.kape.example.com", TLSSecretName: "sre-tls"},
				EgressPolicyRefs: []string{"controller-egress"},
			}
			return s
		}(),
	}
	for name, server := range cases {
		t.Run(name, func(t *testing.T) {
			rendered, err := json.MarshalIndent(agentserver.Render(server, nil), "", "  ")
			require.NoError(t, err)
			rendered = append(rendered, '\n')

			goldenPath := filepath.Join("testdata", name+".golden.json")
			if *update {
				require.NoError(t, os.WriteFile(goldenPath, rendered, 0o644))
			}
			want, err := os.ReadFile(goldenPath)
			require.NoError(t, err, "run with -update to write the golden file")
			assert.JSONEq(t, string(want), string(rendered))
		})
	}
}

// TestRenderDeploymentEnvContract pins the env mapping table (spec #261):
// sandbox.image → KAPE_DEFAULT_IMAGE, sandbox.model → KAPE_DEFAULT_MODEL,
// sandbox.resources → KAPE_DEFAULT_RESOURCES, modelGateway.url →
// KAPE_MODEL_GATEWAY_URL, description → KAPE_SERVER_DESCRIPTION, and
// KAPE_CREDENTIAL_BINDINGS rendered empty until the credential slice (#265).
func TestRenderDeploymentEnvContract(t *testing.T) {
	server := testServer()
	server.Spec.Credentials = []v1alpha1.AgentServerCredentialSpec{{
		Name:         "github",
		SecretRef:    v1alpha1.AgentServerSecretRef{Name: "github-token"},
		ProviderType: "git-https",
		HostPatterns: []string{"github.com:443", "api.github.com:443"},
	}}

	env := agentserver.Render(server, nil).DeploymentEnv
	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	assert.Equal(t, map[string]string{
		"KAPE_DEFAULT_IMAGE":       "ghcr.io/dzungtr/pi-openshell:openrouter",
		"KAPE_DEFAULT_MODEL":       "z-ai/glm-5.2",
		"KAPE_DEFAULT_RESOURCES":   `{"cpu":"1","memory":"2Gi"}`,
		"KAPE_MODEL_GATEWAY_URL":   "http://model-gateway-http.aperture.svc.cluster.local/v1",
		"KAPE_SERVER_DESCRIPTION":  "sre-triage: sandbox agents for the SRE alert pipeline",
		"KAPE_CREDENTIAL_BINDINGS": "[]",
	}, got)
	assert.Len(t, env, 6, "the contract is exactly six env vars; cluster wiring is merged by the apply layer")
}

// TestRenderOmitsInapplicableModel: an empty sandbox.model renders an empty
// value, which the controller treats as "the image's own selection".
func TestRenderOmitsInapplicableModel(t *testing.T) {
	server := testServer()
	server.Spec.Sandbox.Model = ""

	for _, e := range agentserver.Render(server, nil).DeploymentEnv {
		if e.Name == agentserver.EnvDefaultModel {
			assert.Empty(t, e.Value)
			return
		}
	}
	t.Fatalf("KAPE_DEFAULT_MODEL not rendered")
}

// TestRenderOpenShellRecordNaming pins the provider naming convention the
// credential slice consumes (#265, handoff H2).
func TestRenderOpenShellRecordNaming(t *testing.T) {
	server := testServer()
	server.Spec.Credentials = []v1alpha1.AgentServerCredentialSpec{{
		Name:         "github",
		SecretRef:    v1alpha1.AgentServerSecretRef{Name: "github-token"},
		ProviderType: "git-https",
		HostPatterns: []string{"github.com:443", "api.github.com:443"},
	}}

	desired := agentserver.Render(server, agentserver.ResolvedSecrets{
		"github": {"GITHUB_TOKEN": "secret-value"},
	})

	require.Len(t, desired.OpenShellRecords, 1)
	rec := desired.OpenShellRecords[0]
	assert.Equal(t, "kape-kape-system-sre-github", rec.Name)
	assert.Equal(t, "git-https", rec.ProviderType)
	assert.Equal(t, []string{"github.com:443", "api.github.com:443"}, rec.HostPatterns)
	assert.Equal(t, "secret-value", rec.Values["GITHUB_TOKEN"])
	// Credential material never reaches the env contract (ADR-0088).
	for _, e := range desired.DeploymentEnv {
		assert.NotContains(t, e.Value, "secret-value")
	}
}

// TestRenderExcludesUnresolvedCredentials: degrade-loud (D9) — a credential
// whose Secret did not resolve produces no record and blocks nothing else.
func TestRenderExcludesUnresolvedCredentials(t *testing.T) {
	server := testServer()
	server.Spec.Credentials = []v1alpha1.AgentServerCredentialSpec{
		{
			Name:         "github",
			SecretRef:    v1alpha1.AgentServerSecretRef{Name: "github-token"},
			ProviderType: "git-https",
			HostPatterns: []string{"github.com:443"},
		},
		{
			Name:         "broken",
			SecretRef:    v1alpha1.AgentServerSecretRef{Name: "missing-token"},
			ProviderType: "git-https",
			HostPatterns: []string{"gitlab.com:443"},
		},
	}

	desired := agentserver.Render(server, agentserver.ResolvedSecrets{
		"github": {"GITHUB_TOKEN": "secret-value"},
	})

	require.Len(t, desired.OpenShellRecords, 1)
	assert.Equal(t, "kape-kape-system-sre-github", desired.OpenShellRecords[0].Name)
}

// TestRenderServiceAndIngress pins the network resources: the Service always
// exists (it is the controller endpoint), the Ingress only when declared.
func TestRenderServiceAndIngress(t *testing.T) {
	server := testServer()
	desired := agentserver.Render(server, nil)
	require.NotNil(t, desired.NetworkResources.Service)
	assert.Nil(t, desired.NetworkResources.Ingress)
	assert.Equal(t, "kape-controller-sre", desired.NetworkResources.Service.Name)
	assert.Equal(t, "kape-controller-sre", agentserver.ControllerName(server.Name))
	require.Len(t, desired.NetworkResources.Service.Spec.Ports, 1)
	assert.Equal(t, int32(80), desired.NetworkResources.Service.Spec.Ports[0].Port)
	assert.Equal(t, agentserver.ControllerPort, agentserver.ControllerPort)

	server.Spec.Network = &v1alpha1.AgentServerNetworkSpec{
		Ingress: &v1alpha1.AgentServerIngressSpec{Host: "sre.kape.example.com"},
	}
	desired = agentserver.Render(server, nil)
	require.NotNil(t, desired.NetworkResources.Ingress)
	assert.Empty(t, desired.NetworkResources.Ingress.Spec.TLS, "no TLS secret declared")
	backend := desired.NetworkResources.Ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	require.NotNil(t, backend)
	assert.Equal(t, "kape-controller-sre", backend.Name)
}

// TestRenderIsPure: rendering twice yields identical output, and neither the
// input CR nor a shared slice is mutated.
func TestRenderIsPure(t *testing.T) {
	server := testServer()
	server.Spec.Credentials = []v1alpha1.AgentServerCredentialSpec{{
		Name:         "github",
		SecretRef:    v1alpha1.AgentServerSecretRef{Name: "github-token"},
		ProviderType: "git-https",
		HostPatterns: []string{"github.com:443"},
	}}
	secrets := agentserver.ResolvedSecrets{"github": {"GITHUB_TOKEN": "secret-value"}}

	first, err := json.Marshal(agentserver.Render(server, secrets))
	require.NoError(t, err)
	second, err := json.Marshal(agentserver.Render(server, secrets))
	require.NoError(t, err)
	assert.JSONEq(t, string(first), string(second))

	// The record holds a copy of the CR's host patterns, not the backing array.
	rec := agentserver.Render(server, secrets).OpenShellRecords[0]
	rec.HostPatterns[0] = "mutated"
	assert.Equal(t, "github.com:443", server.Spec.Credentials[0].HostPatterns[0])
}

// TestRenderServiceSelectorMatchesPodLabels guards the one coupling the pure
// renderer and the apply layer share: the Service must select rendered pods.
func TestRenderServiceSelectorMatchesPodLabels(t *testing.T) {
	server := testServer()
	svc := agentserver.Render(server, nil).NetworkResources.Service
	assert.Equal(t, agentserver.SelectorLabels(server.Name), svc.Spec.Selector)
	assert.Equal(t, agentserver.ObjectLabels(server.Name)["app.kubernetes.io/managed-by"], "kape-operator")
	assert.Equal(t, server.Name, agentserver.ObjectLabels(server.Name)[agentserver.AgentServerLabel])
	assert.Equal(t, corev1.ProtocolTCP, svc.Spec.Ports[0].Protocol)
}
