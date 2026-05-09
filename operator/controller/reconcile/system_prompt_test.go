package reconcile_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kape-io/kape/operator/controller/reconcile"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

func mkSkill(name, description, instruction string, lazy bool) v1alpha1.KapeSkill {
	return v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kape-system"},
		Spec: v1alpha1.KapeSkillSpec{
			Description: description,
			Instruction: instruction,
			LazyLoad:    lazy,
		},
	}
}

func handlerWithPrompt(prompt string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system"},
		Spec: v1alpha1.KapeHandlerSpec{
			LLM: v1alpha1.LLMSpec{Provider: "p", Model: "m", SystemPrompt: prompt},
		},
	}
}

func TestAssembleSystemPrompt_HandlerOnly(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(handlerWithPrompt("base prompt"), nil, nil)
	assert.Equal(t, "base prompt", got)
}

func TestAssembleSystemPrompt_HandlerPlusEager_Single(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		[]v1alpha1.KapeSkill{mkSkill("s1", "d1", "INSTR-1", false)},
		nil,
	)
	assert.Equal(t, "base\n\n---\n\nINSTR-1", got)
}

func TestAssembleSystemPrompt_HandlerPlusEager_MultipleInDeclarationOrder(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		[]v1alpha1.KapeSkill{
			mkSkill("s1", "d1", "FIRST", false),
			mkSkill("s2", "d2", "SECOND", false),
		},
		nil,
	)
	expected := "base\n\n---\n\nFIRST\n\n---\n\nSECOND"
	assert.Equal(t, expected, got)
}

func TestAssembleSystemPrompt_HandlerPlusLazyOnly(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		nil,
		[]v1alpha1.KapeSkill{
			mkSkill("check-orders", "Investigates order events", "instr-ignored", true),
			mkSkill("check-shifts", "Looks at shift handovers", "instr-ignored", true),
		},
	)
	// Two newlines (not "---") between handler prompt and lazy preamble when no eager skills exist.
	assert.True(t, strings.HasPrefix(got, "base\n\n"))
	assert.False(t, strings.Contains(got, "base\n\n---\n\nAvailable skills"),
		"no separator should be emitted when eager skills are absent")
	assert.Contains(t, got, "Available skills (call load_skill with the skill name to retrieve full instructions):")
	assert.Contains(t, got, "- check-orders: Investigates order events")
	assert.Contains(t, got, "- check-shifts: Looks at shift handovers")
	assert.Contains(t, got, "When you determine a skill is relevant, call load_skill with its name before proceeding.")
	// Lazy instructions must NOT appear in the prompt; only descriptions.
	assert.False(t, strings.Contains(got, "instr-ignored"))
}

func TestAssembleSystemPrompt_HandlerPlusEagerAndLazy_OrderAndSeparators(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		[]v1alpha1.KapeSkill{mkSkill("eager-1", "d", "EAGER-INSTR", false)},
		[]v1alpha1.KapeSkill{mkSkill("lazy-1", "lazy desc", "lazy-instr-ignored", true)},
	)
	// Expected layout: base --- EAGER-INSTR --- Available skills ...
	expectedPrefix := "base\n\n---\n\nEAGER-INSTR\n\n---\n\nAvailable skills"
	assert.True(t, strings.HasPrefix(got, expectedPrefix), "actual prefix:\n%q", got)
	assert.Contains(t, got, "- lazy-1: lazy desc")
	assert.False(t, strings.Contains(got, "lazy-instr-ignored"))
}

func TestAssembleSystemPrompt_DeterministicForSameInputs(t *testing.T) {
	h := handlerWithPrompt("base")
	eager := []v1alpha1.KapeSkill{mkSkill("e1", "d", "E1", false)}
	lazy := []v1alpha1.KapeSkill{mkSkill("l1", "ld", "li", true)}
	a := reconcile.AssembleSystemPrompt(h, eager, lazy)
	b := reconcile.AssembleSystemPrompt(h, eager, lazy)
	assert.Equal(t, a, b)
}

func TestAssembleSystemPrompt_EmptyHandlerPromptIsAllowed(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt(""),
		[]v1alpha1.KapeSkill{mkSkill("e1", "d", "E1-INSTR", false)},
		nil,
	)
	assert.Equal(t, "\n\n---\n\nE1-INSTR", got)
}
