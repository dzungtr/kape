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

// Credential Secret gating (ADR-0088, D7/D10): the operator reads only Secrets
// an admin explicitly labelled for credential transit, and only in the
// KapeAgentServer's own namespace.
const (
	CredentialLabel      = "kape.io/credential"
	CredentialLabelValue = "true"
)

// ControllerPort is the controller's HTTP/MCP container port (controller
// Config.ListenAddr default ":8081").
const ControllerPort int32 = 8081

// ControllerName is the name shared by the controller Deployment, its Service,
// and (suffixed) the Ingress: one CR materialises one controller.
func ControllerName(serverName string) string { return "kape-controller-" + serverName }

// ResolvedSecrets holds the credential Secret material read by the operator's
// secret resolver (M4, #265), keyed by credentials[].name, then by credential
// key. The values are secret material: they feed provider records and nothing
// else — they never reach the CR, the env contract, or a log line (ADR-0088).
type ResolvedSecrets map[string]map[string]string

// OpenShellRecord is the provider record the operator writes for one healthy
// credential (M3, #265): one CR credential, one provider, named by
// ProviderName. Credential is the credentials[].name the record came from, so
// status conditions can name the CR entry the gateway rejected.
type OpenShellRecord struct {
	Credential   string
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
	// ProviderProfiles are the provider-type profile documents the writer must
	// import before it creates any provider, deduplicated by id in order of
	// first appearance. A credential whose providerType has no document
	// contributes no profile — the writer reports it instead (D9).
	ProviderProfiles []ProviderProfile
	// OpenShellRecords are the provider records the writer converges, one per
	// credential present in resolvedSecrets. Credentials that did not resolve
	// (or did not push) are absent, and absent means absent from
	// KAPE_CREDENTIAL_BINDINGS too: the controller never binds an endpoint to a
	// provider that is not there.
	OpenShellRecords []OpenShellRecord
}

// Render maps a KapeAgentServer to its DesiredState. Same input, same output.
//
// resolvedSecrets is the credential material that has been resolved *and* — on
// the reconcile's second pass — successfully pushed to the gateway. Rendering
// with the pushed subset is what keeps KAPE_CREDENTIAL_BINDINGS honest: it
// carries exactly the providers the controller may bind (D9).
func Render(server *v1alpha1.KapeAgentServer, resolvedSecrets ResolvedSecrets) DesiredState {
	records := openShellRecords(server, resolvedSecrets)
	return DesiredState{
		DeploymentEnv:    deploymentEnv(server.Spec, records),
		NetworkResources: networkResources(server),
		ProviderProfiles: providerProfiles(records),
		OpenShellRecords: records,
	}
}

// deploymentEnv renders the env contract in a fixed order so the golden output
// is stable across reconciles.
func deploymentEnv(spec v1alpha1.KapeAgentServerSpec, records []OpenShellRecord) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: EnvDefaultImage, Value: spec.Sandbox.Image},
		{Name: EnvDefaultModel, Value: spec.Sandbox.Model},
		{Name: EnvDefaultResources, Value: resourcesJSON(spec.Sandbox.Resources)},
		{Name: EnvModelGatewayURL, Value: spec.ModelGateway.URL},
		{Name: EnvServerDescription, Value: spec.Description},
		{Name: EnvCredentialBindings, Value: credentialBindingsJSON(records)},
	}
}

// credentialBinding is one entry of KAPE_CREDENTIAL_BINDINGS in the shape the
// controller parses (handoff H1): the provider name and the egress host
// patterns the credential is valid for.
type credentialBinding struct {
	Provider string   `json:"provider"`
	Hosts    []string `json:"hosts"`
}

// credentialBindingsJSON renders the binding table: provider names and hosts
// only, in credential order, with no secret material in it.
func credentialBindingsJSON(records []OpenShellRecord) string {
	bindings := make([]credentialBinding, 0, len(records))
	for _, rec := range records {
		bindings = append(bindings, credentialBinding{
			Provider: rec.Name,
			Hosts:    append([]string(nil), rec.HostPatterns...),
		})
	}
	b, err := json.Marshal(bindings)
	if err != nil {
		// Unreachable: strings and string slices always marshal. Returning ""
		// would hand the controller an unparsable table, so panic loudly.
		panic(fmt.Sprintf("marshal credential bindings: %v", err))
	}
	return string(b)
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
			Credential:   cred.Name,
			Name:         ProviderName(server.Namespace, server.Name, cred.Name),
			ProviderType: cred.ProviderType,
			HostPatterns: append([]string(nil), cred.HostPatterns...),
			Values:       copyValues(values),
		})
	}
	return records
}

// providerProfiles collects the profile documents the records need, one per
// provider type, in order of first appearance. A provider type with no shipped
// document is skipped here and reported by the writer.
func providerProfiles(records []OpenShellRecord) []ProviderProfile {
	var profiles []ProviderProfile
	seen := map[string]bool{}
	for _, rec := range records {
		if seen[rec.ProviderType] {
			continue
		}
		profile, ok := Profile(rec.ProviderType)
		if !ok {
			continue
		}
		seen[rec.ProviderType] = true
		profiles = append(profiles, profile)
	}
	return profiles
}

// copyValues copies resolved secret material so the rendered state never aliases
// the resolver's map.
func copyValues(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}
