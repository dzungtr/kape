package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SkillToolRef references a KapeTool by name within a KapeSkill.
type SkillToolRef struct {
	// Ref is the name of the KapeTool in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// KapeSkillSpec defines the desired state of a KapeSkill.
type KapeSkillSpec struct {
	// Description is a human-readable summary of what this skill does.
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Instruction is the system-prompt text injected when this skill is eager-loaded.
	// +kubebuilder:validation:MinLength=1
	Instruction string `json:"instruction"`

	// Tools is the list of KapeTools this skill requires.
	// +optional
	Tools []SkillToolRef `json:"tools,omitempty"`

	// LazyLoad defers injection of this skill's instruction until explicitly invoked.
	// When false (default), the instruction is included in the handler's system prompt at startup.
	// +optional
	// +kubebuilder:default=false
	LazyLoad bool `json:"lazyLoad,omitempty"`
}

// KapeSkillStatus defines the observed state of a KapeSkill.
type KapeSkillStatus struct {
	// Conditions represent the latest available observations of the skill's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// KapeSkill groups a reusable instruction + tool set that can be attached to one or
// more KapeHandlers.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kape
// +kubebuilder:printcolumn:name="LazyLoad",type=boolean,JSONPath=`.spec.lazyLoad`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type KapeSkill struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KapeSkillSpec   `json:"spec,omitempty"`
	Status KapeSkillStatus `json:"status,omitempty"`
}

// KapeSkillList contains a list of KapeSkill resources.
//
// +kubebuilder:object:root=true
type KapeSkillList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []KapeSkill `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KapeSkill{}, &KapeSkillList{})
}
