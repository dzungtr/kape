package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SandboxResources is the default sandbox resource profile, in the shape the
// controller consumes as KAPE_DEFAULT_RESOURCES: a JSON object with Kubernetes
// cpu/memory quantity strings.
type SandboxResources struct {
	// CPU is a Kubernetes CPU quantity (e.g. "1", "500m").
	// +kubebuilder:validation:Pattern=`^[0-9]+([.][0-9]+)?m?$`
	CPU string `json:"cpu"`

	// Memory is a Kubernetes memory quantity (e.g. "2Gi", "512Mi").
	// +kubebuilder:validation:Pattern=`^[0-9]+([.][0-9]+)?(E|P|T|G|M|K|Ei|Pi|Ti|Gi|Mi|Ki)?$`
	Memory string `json:"memory"`
}

// AgentServerControllerSpec is the kape-controller image and resource profile
// the operator materialises as one Deployment in the CR's namespace.
type AgentServerControllerSpec struct {
	// Image is the kape-controller OCI image reference.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Resources overrides the controller container's requests/limits.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AgentServerSandboxSpec holds the defaults every sandbox created through this
// controller inherits.
type AgentServerSandboxSpec struct {
	// Image is the default sandbox agent image (KAPE_DEFAULT_IMAGE).
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Model is the default model id handed to the sandbox agent
	// (KAPE_DEFAULT_MODEL). Empty keeps the sandbox image's own selection.
	// +optional
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._:/-]+$`
	Model string `json:"model,omitempty"`

	// Resources is the default sandbox cpu/memory profile
	// (KAPE_DEFAULT_RESOURCES).
	Resources SandboxResources `json:"resources"`
}

// AgentServerModelGatewaySpec points the controller at the inference gateway.
type AgentServerModelGatewaySpec struct {
	// URL is the model-gateway base URL patched into each sandbox's models.json
	// (KAPE_MODEL_GATEWAY_URL).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^https?://.+`
	URL string `json:"url"`
}

// AgentServerSecretRef names the Secret holding one credential's material. Only
// the name is carried: credential Secrets are same-namespace by construction
// (D10, #261) — the operator never reads across namespaces.
type AgentServerSecretRef struct {
	// Name is the Secret name in the KapeAgentServer's own namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is rejected at admission whenever set. It exists only so a
	// cross-namespace reference fails with a targeted message instead of
	// surfacing as an unknown field; credential Secrets are same-namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// AgentServerCredentialSpec is one entry of the generic credential registry
// (ADR-0088): a named credential, the Secret that holds it, the OpenShell
// provider type it is imported as, and the egress hosts it is bound to.
//
// +kubebuilder:validation:XValidation:rule="self.hostPatterns.all(h, h.matches('^([*]{1,2}|[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?)([.]([*]{1,2}|[A-Za-z0-9]([-A-Za-z0-9]*[A-Za-z0-9])?))*(:[0-9]{1,5})?$'))",message="hostPatterns entries must be host[:port] globs in OpenShell NetworkEndpoint syntax (e.g. github.com:443, *.example.com:443, **.example.com:443)"
// +kubebuilder:validation:XValidation:rule="!has(self.secretRef.namespace)",message="secretRef must be same-namespace: omit namespace (credential Secrets are read from the KapeAgentServer namespace only)"
type AgentServerCredentialSpec struct {
	// Name identifies the credential within this KapeAgentServer. It is part of
	// the OpenShell provider name the operator derives, so it is a DNS label.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// SecretRef names the same-namespace Secret holding the credential value.
	SecretRef AgentServerSecretRef `json:"secretRef"`

	// ProviderType is the OpenShell provider type the credential is imported as
	// (e.g. "git-https"). The registry is generic (D2) — new types are config.
	// +kubebuilder:validation:MinLength=1
	ProviderType string `json:"providerType"`

	// HostPatterns are the egress hosts the credential is bound to, in
	// OpenShell NetworkEndpoint syntax (host[:port] globs).
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=253
	HostPatterns []string `json:"hostPatterns"`
}

// AgentServerIngressSpec publishes the controller endpoint outside the cluster.
type AgentServerIngressSpec struct {
	// Host is the DNS name the controller is served under.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?([.][a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Host string `json:"host"`

	// TLSSecretName is the same-namespace Secret holding the serving
	// certificate (kubernetes.io/tls). Empty serves plain HTTP.
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`
}

// AgentServerNetworkSpec declares the network surface of the serving
// environment.
type AgentServerNetworkSpec struct {
	// Ingress publishes the controller's MCP + REST endpoint under Host.
	// +optional
	Ingress *AgentServerIngressSpec `json:"ingress,omitempty"`

	// EgressPolicyRefs names same-namespace NetworkPolicy resources that define
	// the controller pod's additional egress. Declared now so the schema needs no
	// revision; the controller egress path is reconciled by the credential
	// slices (#264/#265), which render per-endpoint egress from credentials[].
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=253
	EgressPolicyRefs []string `json:"egressPolicyRefs,omitempty"`
}

// KapeAgentServerSpec declares one project serving environment (ADR-0087): the
// controller image, sandbox defaults, model gateway, credential registry,
// network surface, and free-text self-description.
//
// +kubebuilder:validation:XValidation:rule="self.credentials.all(c1, self.credentials.exists_one(c2, c2.name == c1.name))",message="credentials[].name must be unique within a KapeAgentServer"
type KapeAgentServerSpec struct {
	// Description is the serving environment's self-description, served to MCP
	// clients as the server `instructions` of the initialize handshake
	// (KAPE_SERVER_DESCRIPTION). This is how callers route between several
	// KapeAgentServers exposing the same tool contract (D6).
	// +optional
	// +kubebuilder:validation:MaxLength=8192
	Description string `json:"description,omitempty"`

	// Controller is the kape-controller this CR materialises.
	Controller AgentServerControllerSpec `json:"controller"`

	// Sandbox holds the defaults every sandbox created here inherits.
	Sandbox AgentServerSandboxSpec `json:"sandbox"`

	// ModelGateway points at the inference gateway.
	ModelGateway AgentServerModelGatewaySpec `json:"modelGateway"`

	// Credentials is the generic credential registry (ADR-0088).
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Credentials []AgentServerCredentialSpec `json:"credentials,omitempty"`

	// Network declares the serving environment's network surface.
	// +optional
	Network *AgentServerNetworkSpec `json:"network,omitempty"`
}

// AgentServerCredentialStatus reports one credential's provisioning outcome.
// Nothing writes it until the credential-automation slice (#265); the schema
// ships now so that slice needs no CRD revision.
type AgentServerCredentialStatus struct {
	// Name is the credentials[].name this status describes.
	Name string `json:"name"`

	// Conditions report the credential's provisioning state (e.g. Pushed).
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// KapeAgentServerStatus defines the observed state of a KapeAgentServer.
type KapeAgentServerStatus struct {
	// ObservedGeneration is the metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions carry the aggregate Ready condition, plus Degraded once the
	// credential slices land (D9).
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Credentials reports per-credential provisioning state (written by #265).
	// +optional
	Credentials []AgentServerCredentialStatus `json:"credentials,omitempty"`
}

// KapeAgentServer is the declarative serving-environment CR (ADR-0087): one CR
// materialises one kape-controller Deployment (env-configured tool surface) plus
// the network resources in the CR's namespace.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kape,shortName=kas
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.controller.image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type KapeAgentServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KapeAgentServerSpec   `json:"spec,omitempty"`
	Status KapeAgentServerStatus `json:"status,omitempty"`
}

// KapeAgentServerList contains a list of KapeAgentServer resources.
//
// +kubebuilder:object:root=true
type KapeAgentServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []KapeAgentServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KapeAgentServer{}, &KapeAgentServerList{})
}
