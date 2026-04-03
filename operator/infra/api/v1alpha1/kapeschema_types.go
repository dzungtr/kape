// Package v1alpha1 contains API Schema definitions for the kape.io v1alpha1 API group.
package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JSONSchemaObject defines the JSON Schema for structured LLM output.
type JSONSchemaObject struct {
	// type must be "object".
	// +kubebuilder:validation:Enum=object
	Type string `json:"type"`

	// required lists the required properties. Must have at least one entry.
	// +kubebuilder:validation:MinItems=1
	Required []string `json:"required"`

	// properties defines the schema properties as a map of raw JSON schema objects.
	// +kubebuilder:pruning:PreserveUnknownFields
	Properties map[string]apiextensionsv1.JSON `json:"properties"`

	// additionalProperties when set to false disallows any properties not listed in properties.
	// +optional
	AdditionalProperties *bool `json:"additionalProperties,omitempty"`
}

// KapeSchemaSpec defines the desired state of KapeSchema.
// +kubebuilder:validation:XValidation:rule="self.version.matches('^v[0-9]+$')",message="spec.version must match pattern v[0-9]+ (e.g. v1, v2, v10)"
type KapeSchemaSpec struct {
	// version is the schema version identifier (e.g. v1, v2).
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// jsonSchema is the JSON Schema definition for structured LLM output.
	JSONSchema JSONSchemaObject `json:"jsonSchema"`
}

// KapeSchemaStatus defines the observed state of KapeSchema.
type KapeSchemaStatus struct {
	// conditions holds the conditions for the KapeSchema.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kape
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KapeSchema defines the structured output contract for LLM decisions.
type KapeSchema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KapeSchemaSpec   `json:"spec,omitempty"`
	Status KapeSchemaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KapeSchemaList contains a list of KapeSchema.
type KapeSchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KapeSchema `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KapeSchema{}, &KapeSchemaList{})
}
