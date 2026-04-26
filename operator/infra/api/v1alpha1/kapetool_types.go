package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MCPUpstreamSpec defines the upstream MCP server connection details.
type MCPUpstreamSpec struct {
	// Transport is the MCP transport protocol.
	// +kubebuilder:validation:Enum=sse;streamable-http
	Transport string `json:"transport"`

	// URL is the upstream MCP server URL.
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`
}

// JSONPathRule defines a JSONPath-based redaction rule.
type JSONPathRule struct {
	// JSONPath is the JSONPath expression identifying the field to redact.
	JSONPath string `json:"jsonPath"`
}

// RedactionSpec defines input/output redaction rules for MCP tool calls.
type RedactionSpec struct {
	// Input is the list of JSONPath rules applied to tool call inputs.
	// +optional
	Input []JSONPathRule `json:"input,omitempty"`

	// Output is the list of JSONPath rules applied to tool call outputs.
	// +optional
	Output []JSONPathRule `json:"output,omitempty"`
}

// AuditSpec defines audit logging configuration for MCP tool calls.
type AuditSpec struct {
	// Enabled controls per-call audit logging. Defaults to true. Always true in v1.
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`
}

// MCPSpec defines the configuration for an MCP server sidecar tool.
type MCPSpec struct {
	// Upstream defines the MCP server connection details.
	Upstream MCPUpstreamSpec `json:"upstream"`

	// AllowedTools is an allowlist of MCP tool names to expose.
	// +optional
	AllowedTools []string `json:"allowedTools,omitempty"`

	// DeniedTools is a denylist of MCP tool names to block.
	// +optional
	DeniedTools []string `json:"deniedTools,omitempty"`

	// Redaction defines field-level redaction rules for tool call I/O.
	// +optional
	Redaction *RedactionSpec `json:"redaction,omitempty"`

	// Audit defines audit logging configuration.
	// +optional
	Audit *AuditSpec `json:"audit,omitempty"`

	// SkipProbe disables the operator's HTTP health probe against the upstream MCP server.
	// MCP does not require servers to expose a /health endpoint, so set this to true when the
	// upstream lacks one — without it the tool stays Ready=False / MCPEndpointUnreachable forever.
	// When skipped, Ready is set True with Reason=ProbeSkipped.
	// +optional
	SkipProbe bool `json:"skipProbe,omitempty"`
}

// MemorySpec defines the configuration for a vector memory backend tool.
type MemorySpec struct {
	// Backend is the vector memory backend type.
	// +kubebuilder:validation:Enum=qdrant;pgvector;weaviate
	Backend string `json:"backend"`

	// DistanceMetric is the distance function used for vector similarity search.
	// +kubebuilder:validation:Enum=cosine;dot;euclidean
	DistanceMetric string `json:"distanceMetric"`
}

// EventPublishSpec defines the configuration for an event-publish contract tool.
type EventPublishSpec struct {
	// Type is the CloudEvent type for events published by this tool.
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// Source is the optional CloudEvent source URI.
	// +optional
	Source string `json:"source,omitempty"`
}

// KapeToolSpec defines the desired state of a KapeTool.
//
// +kubebuilder:validation:XValidation:rule="self.type != 'memory' || (has(self.memory) && self.memory.backend in ['qdrant', 'pgvector', 'weaviate'])",message="spec.memory.backend must be one of: qdrant, pgvector, weaviate"
// +kubebuilder:validation:XValidation:rule="self.type != 'memory' || (has(self.memory) && self.memory.distanceMetric in ['cosine', 'dot', 'euclidean'])",message="spec.memory.distanceMetric must be one of: cosine, dot, euclidean"
// +kubebuilder:validation:XValidation:rule="self.type != 'event-publish' || (has(self.eventPublish) && self.eventPublish.type.startsWith('kape.events.'))",message="spec.eventPublish.type must start with 'kape.events.'"
// +kubebuilder:validation:XValidation:rule="!(self.type == 'mcp' && has(self.mcp) && size(self.mcp.allowedTools) > 0 && has(self.mcp.deniedTools) && size(self.mcp.deniedTools) > 0)",message="spec.mcp: allowedTools and deniedTools cannot both be set"
type KapeToolSpec struct {
	// Description is an optional human-readable description of this tool.
	// +optional
	Description string `json:"description,omitempty"`

	// Type is the tool capability category.
	// +kubebuilder:validation:Enum=mcp;memory;event-publish
	Type string `json:"type"`

	// MCP defines configuration for an MCP server sidecar tool.
	// +optional
	MCP *MCPSpec `json:"mcp,omitempty"`

	// Memory defines configuration for a vector memory backend tool.
	// +optional
	Memory *MemorySpec `json:"memory,omitempty"`

	// EventPublish defines configuration for an event-publish contract tool.
	// +optional
	EventPublish *EventPublishSpec `json:"eventPublish,omitempty"`
}

// KapeToolStatus defines the observed state of a KapeTool.
type KapeToolStatus struct {
	// Conditions represent the latest available observations of the tool's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// QdrantEndpoint is the Qdrant HTTP endpoint for memory-type tools.
	// Written after the StatefulSet reaches ReadyReplicas >= 1.
	// +optional
	QdrantEndpoint string `json:"qdrantEndpoint,omitempty"`
}

// KapeTool registers a tool capability — either an MCP server sidecar, a vector memory backend,
// or an event-publish contract. The operator reads KapeTool CRDs to provision infrastructure
// for handler pods.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kape
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type KapeTool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KapeToolSpec   `json:"spec,omitempty"`
	Status KapeToolStatus `json:"status,omitempty"`
}

// KapeToolList contains a list of KapeTool resources.
//
// +kubebuilder:object:root=true
type KapeToolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []KapeTool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KapeTool{}, &KapeToolList{})
}
