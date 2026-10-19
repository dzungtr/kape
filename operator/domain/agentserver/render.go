// Package agentserver renders a KapeAgentServer CR into the desired state the
// operator applies (ADR-0087, spec #261 M2).
//
// Everything here is pure: no client, no clock, no cluster. The apply layer
// (operator/infra/k8s.AgentServerAdapter) owns the impure half — it builds the
// controller Deployment, merges the operator-level cluster-wiring env, and
// converges the objects in the cluster.
package agentserver

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// Env var names of the operator→controller contract (D5). The controller
// consumes them verbatim and learns nothing else from the CR.
const (
	EnvDefaultImage       = "KAPE_DEFAULT_IMAGE"
	EnvDefaultModel       = "KAPE_DEFAULT_MODEL"
	EnvDefaultResources   = "KAPE_DEFAULT_RESOURCES"
	EnvModelGatewayURL    = "KAPE_MODEL_GATEWAY_URL"
	EnvServerDescription  = "KAPE_SERVER_DESCRIPTION"
	EnvCredentialBindings = "KAPE_CREDENTIAL_BINDINGS"
)

// Ownership labels stamped on every object rendered from a KapeAgentServer. The
// apply layer will not patch an object without them — it never adopts.
const (
	ManagedByLabel   = "app.kubernetes.io/managed-by"
	ManagedByValue   = "kape-operator"
	AgentServerLabel = "kape.io/agentserver"
)

// ControllerPort is the controller's HTTP/MCP container port (controller
// Config.ListenAddr default ":8081").
const ControllerPort int32 = 8081

// ControllerName is the name shared by the controller Deployment, its Service,
// and (suffixed) the Ingress: one CR materialises one controller.
func ControllerName(serverName string) string { return "kape-controller-" + serverName }

// ResolvedSecrets holds the credential Secret material read by the operator's
// secret resolver (M4, #265), keyed by credentials[].name. Empty until the
// credential-automation slice: this slice renders no credential bindings.
type ResolvedSecrets map[string]map[string]string

// OpenShellRecord is the provider record the operator writes for one healthy
// credential (M3, #265). Name is both the imported provider-profile name and
// the provider name — one CR credential, one record.
type OpenShellRecord struct {
	Name         string
	ProviderType string
	HostPatterns []string
	Values       map[string]string
}

// NetworkResources are the non-Deployment objects a KapeAgentServer renders.
type NetworkResources struct {
	// Service fronts the controller pod in the CR's namespace.
	Service *corev1.Service
	// Ingress publishes the controller under spec.network.ingress; nil when the
	// CR declares no ingress.
	Ingress *networkingv1.Ingress
}

// DesiredState is the complete rendered state of one KapeAgentServer.
type DesiredState struct {
	// DeploymentEnv is the CR-derived env contract for the controller container.
	// The apply layer appends the operator-level cluster wiring, which is
	// deployment configuration, not project intent.
	DeploymentEnv []corev1.EnvVar
	// NetworkResources are the rendered Service (and Ingress when declared).
	NetworkResources NetworkResources
	// OpenShellRecords are the provider records the credential slices write
	// (M3, #265). Render always produces them from resolvedSecrets; this slice
	// passes none, so the list is empty and KAPE_CREDENTIAL_BINDINGS is [].
	OpenShellRecords []OpenShellRecord
}

// Render maps a KapeAgentServer to its DesiredState. Same input, same output.
//
// KAPE_CREDENTIAL_BINDINGS is rendered empty ("[]") for every CR: the
// credential-automation slice (#265) is what fills it. resolvedSecrets is
// therefore unused this slice and takes no part in the env contract.
func Render(server *v1alpha1.KapeAgentServer, resolvedSecrets ResolvedSecrets) DesiredState {
	spec := server.Spec
	return DesiredState{
		DeploymentEnv:    deploymentEnv(spec),
		NetworkResources: networkResources(server),
		OpenShellRecords: openShellRecords(server, resolvedSecrets),
	}
}

// deploymentEnv renders the env contract in a fixed order so the golden output
// is stable across reconciles.
func deploymentEnv(spec v1alpha1.KapeAgentServerSpec) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: EnvDefaultImage, Value: spec.Sandbox.Image},
		{Name: EnvDefaultModel, Value: spec.Sandbox.Model},
		{Name: EnvDefaultResources, Value: resourcesJSON(spec.Sandbox.Resources)},
		{Name: EnvModelGatewayURL, Value: spec.ModelGateway.URL},
		{Name: EnvServerDescription, Value: spec.Description},
		{Name: EnvCredentialBindings, Value: "[]"},
	}
}

// resourcesJSON is the KAPE_DEFAULT_RESOURCES wire shape the controller parses:
// {"cpu":"...","memory":"..."} (controller/config.go Resources).
func resourcesJSON(r v1alpha1.SandboxResources) string {
	b, err := json.Marshal(struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	}{CPU: r.CPU, Memory: r.Memory})
	if err != nil {
		// Unreachable: two strings cannot fail to marshal. Returning "" would
		// hand the controller an unparsable value, so panic loudly instead.
		panic(fmt.Sprintf("marshal sandbox resources: %v", err))
	}
	return string(b)
}

func networkResources(server *v1alpha1.KapeAgentServer) NetworkResources {
	res := NetworkResources{Service: service(server)}
	if server.Spec.Network != nil && server.Spec.Network.Ingress != nil {
		res.Ingress = ingress(server, server.Spec.Network.Ingress)
	}
	return res
}

// SelectorLabels are the pod/Service selector labels for one CR.
func SelectorLabels(serverName string) map[string]string {
	return map[string]string{
		AgentServerLabel:         serverName,
		"app.kubernetes.io/name": ControllerName(serverName),
	}
}

// ObjectLabels are the ownership labels the apply layer checks before
// patching or deleting an object.
func ObjectLabels(serverName string) map[string]string {
	labels := SelectorLabels(serverName)
	labels[ManagedByLabel] = ManagedByValue
	return labels
}

// OwnerRef is the controller owner reference every rendered object carries.
func OwnerRef(server *v1alpha1.KapeAgentServer) metav1.OwnerReference {
	return *metav1.NewControllerRef(server, v1alpha1.GroupVersion.WithKind("KapeAgentServer"))
}

func service(server *v1alpha1.KapeAgentServer) *corev1.Service {
	name := ControllerName(server.Name)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       server.Namespace,
			Labels:          ObjectLabels(server.Name),
			OwnerReferences: []metav1.OwnerReference{OwnerRef(server)},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: SelectorLabels(server.Name),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       80,
				TargetPort: intstr.FromString("http"),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

func ingress(server *v1alpha1.KapeAgentServer, spec *v1alpha1.AgentServerIngressSpec) *networkingv1.Ingress {
	name := ControllerName(server.Name)
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       server.Namespace,
			Labels:          ObjectLabels(server.Name),
			OwnerReferences: []metav1.OwnerReference{OwnerRef(server)},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: spec.Host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if spec.TLSSecretName != "" {
		ing.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      []string{spec.Host},
			SecretName: spec.TLSSecretName,
		}}
	}
	return ing
}

// ProviderName is the OpenShell record name for one credential:
// kape-<crNamespace>-<crName>-<credName>. The CR namespace and name are in the
// name because OpenShell provider names are gateway-global.
func ProviderName(serverNamespace, serverName, credentialName string) string {
	return fmt.Sprintf("kape-%s-%s-%s", serverNamespace, serverName, credentialName)
}

// openShellRecords renders one record per credential whose Secret resolved.
// Credentials without resolved values are excluded — degrade-loud (D9): a
// missing credential never blocks the healthy rest of the profile.
func openShellRecords(server *v1alpha1.KapeAgentServer, resolved ResolvedSecrets) []OpenShellRecord {
	var records []OpenShellRecord
	for _, cred := range server.Spec.Credentials {
		values, ok := resolved[cred.Name]
		if !ok {
			continue
		}
		records = append(records, OpenShellRecord{
			Name:         ProviderName(server.Namespace, server.Name, cred.Name),
			ProviderType: cred.ProviderType,
			HostPatterns: append([]string(nil), cred.HostPatterns...),
			Values:       values,
		})
	}
	return records
}
