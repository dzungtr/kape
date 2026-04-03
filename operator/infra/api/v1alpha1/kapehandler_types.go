package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TriggerSpec defines the event source configuration for a KapeHandler.
type TriggerSpec struct {
	// Source is the NATS subject or event source identifier.
	// +kubebuilder:validation:MinLength=1
	Source string `json:"source"`

	// Type is the event type to subscribe to.
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// Filter is an optional event filter based on JSONPath expression.
	// +optional
	Filter *EventFilter `json:"filter,omitempty"`

	// Dedup is optional deduplication configuration.
	// +optional
	Dedup *DedupSpec `json:"dedup,omitempty"`

	// ReplayOnStartup controls whether events are replayed on handler startup.
	// +optional
	// +kubebuilder:default=true
	ReplayOnStartup *bool `json:"replayOnStartup,omitempty"`

	// MaxEventAgeSeconds is the maximum age of events to process, in seconds.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=300
	// +optional
	MaxEventAgeSeconds int64 `json:"maxEventAgeSeconds,omitempty"`
}

// EventFilter defines a filter rule based on a JSONPath expression.
type EventFilter struct {
	// Jsonpath is the JSONPath expression to evaluate.
	// +kubebuilder:validation:MinLength=1
	Jsonpath string `json:"jsonpath"`

	// Matches is the expected value the JSONPath expression must match.
	// +kubebuilder:validation:MinLength=1
	Matches string `json:"matches"`
}

// DedupSpec defines deduplication settings for events.
type DedupSpec struct {
	// WindowSeconds is the time window in seconds within which duplicate events are suppressed.
	// +kubebuilder:validation:Minimum=1
	WindowSeconds int64 `json:"windowSeconds"`

	// Key is the event field used to identify duplicate events.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// LLMSpec defines the LLM configuration for the agent.
type LLMSpec struct {
	// Provider is the LLM provider name (e.g. "openai", "anthropic").
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`

	// Model is the LLM model identifier.
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// SystemPrompt is the system-level instruction for the agent.
	// +kubebuilder:validation:MinLength=1
	SystemPrompt string `json:"systemPrompt"`

	// MaxIterations is the maximum number of ReAct loop iterations.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=50
	// +optional
	MaxIterations int32 `json:"maxIterations,omitempty"`

	// ConfidenceThreshold is the minimum confidence score required before executing actions.
	// Must be 0.0 (unset) or between 0.6 and 1.0.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +optional
	ConfidenceThreshold float64 `json:"confidenceThreshold,omitempty"`
}

// ToolRef references a tool available to the agent.
type ToolRef struct {
	// Ref is the name or identifier of the tool.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// ActionSpec defines a deterministic post-decision action.
type ActionSpec struct {
	// Name is a unique identifier for this action.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Condition is a CEL expression that must evaluate to true for this action to execute.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Condition string `json:"condition"`

	// Type specifies the action implementation.
	// +kubebuilder:validation:Enum=event-emitter;save-memory;webhook
	Type string `json:"type"`

	// Data holds arbitrary action-specific configuration.
	// +kubebuilder:pruning:PreserveUnknownFields
	Data map[string]apiextensionsv1.JSON `json:"data"`
}

// ScalingSpec defines the autoscaling configuration for the handler.
type ScalingSpec struct {
	// MinReplicas is the minimum number of handler replicas.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of handler replicas.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=10
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// ScaleToZero enables scaling the handler down to zero replicas when idle.
	// +kubebuilder:default=false
	// +optional
	ScaleToZero bool `json:"scaleToZero,omitempty"`

	// NatsLagThreshold is the NATS consumer lag that triggers a scale-up event.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=5
	// +optional
	NatsLagThreshold int32 `json:"natsLagThreshold,omitempty"`

	// ScaleDownStabilizationSeconds is the period to wait before scaling down.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=60
	// +optional
	ScaleDownStabilizationSeconds int32 `json:"scaleDownStabilizationSeconds,omitempty"`
}

// KapeHandlerSpec defines the desired state of a KapeHandler.
// +kubebuilder:validation:XValidation:rule="self.llm.confidenceThreshold == 0.0 || (self.llm.confidenceThreshold >= 0.6 && self.llm.confidenceThreshold <= 1.0)",message="spec.llm.confidenceThreshold must be between 0.6 and 1.0 (or unset)"
type KapeHandlerSpec struct {
	// Trigger defines the event source this handler subscribes to.
	Trigger TriggerSpec `json:"trigger"`

	// LLM defines the language model configuration for the agent.
	LLM LLMSpec `json:"llm"`

	// Tools is the list of tools available to the agent.
	Tools []ToolRef `json:"tools"`

	// SchemaRef is the name of the schema resource that describes event payloads.
	// +kubebuilder:validation:MinLength=1
	SchemaRef string `json:"schemaRef"`

	// Actions is the list of post-decision actions the agent may execute.
	Actions []ActionSpec `json:"actions"`

	// Envs is a list of additional environment variables injected into the handler pod.
	// +optional
	Envs []corev1.EnvVar `json:"envs,omitempty"`

	// DryRun disables actual action execution; the agent runs but actions are no-ops.
	// +kubebuilder:default=false
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// Scaling defines the autoscaling configuration for the handler.
	// +optional
	Scaling *ScalingSpec `json:"scaling,omitempty"`
}

// KapeHandlerStatus defines the observed state of a KapeHandler.
type KapeHandlerStatus struct {
	// Conditions represent the latest available observations of the handler's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Replicas is the number of currently running handler replicas.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// EventsProcessed is the total number of events processed by this handler.
	// +optional
	EventsProcessed int64 `json:"eventsProcessed,omitempty"`

	// LastProcessed is the timestamp of the most recently processed event.
	// +optional
	LastProcessed *metav1.Time `json:"lastProcessed,omitempty"`

	// LlmLatencyP99Ms is the 99th-percentile LLM call latency in milliseconds.
	// +optional
	LlmLatencyP99Ms int64 `json:"llmLatencyP99Ms,omitempty"`

	// LastError is the most recent error message encountered during processing.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// KapeHandler is the primary CRD — one KapeHandler = one agent pipeline.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kape
// +kubebuilder:printcolumn:name="Schema",type=string,JSONPath=`.spec.schemaRef`
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.llm.provider`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type KapeHandler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KapeHandlerSpec   `json:"spec,omitempty"`
	Status KapeHandlerStatus `json:"status,omitempty"`
}

// KapeHandlerList contains a list of KapeHandler resources.
//
// +kubebuilder:object:root=true
type KapeHandlerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []KapeHandler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KapeHandler{}, &KapeHandlerList{})
}
