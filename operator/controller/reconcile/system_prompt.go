package reconcile

import (
	"strings"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

const skillSeparator = "\n\n---\n\n"

// AssembleSystemPrompt builds the final system prompt per spec 0013 §3.2.
//
// Layout:
//
//  1. handler.spec.llm.systemPrompt (verbatim)
//  2. "\n\n---\n\n"  -- only if eagerSkills is non-empty
//  3. eager skill instructions joined by "\n\n---\n\n", in slice order
//  4. "\n\n---\n\n"  -- only if both eager and lazy skills exist
//     (or "\n\n" if only lazy exists)
//  5. lazy skill preamble — only if lazySkills is non-empty
//
// AssembleSystemPrompt is pure: same inputs always produce the same output,
// no I/O, no time/randomness.
func AssembleSystemPrompt(handler *v1alpha1.KapeHandler, eagerSkills []v1alpha1.KapeSkill, lazySkills []v1alpha1.KapeSkill) string {
	var b strings.Builder
	b.WriteString(handler.Spec.LLM.SystemPrompt)

	if len(eagerSkills) > 0 {
		b.WriteString(skillSeparator)
		eagerInstructions := make([]string, 0, len(eagerSkills))
		for _, s := range eagerSkills {
			eagerInstructions = append(eagerInstructions, s.Spec.Instruction)
		}
		b.WriteString(strings.Join(eagerInstructions, skillSeparator))
	}

	if len(lazySkills) > 0 {
		if len(eagerSkills) > 0 {
			b.WriteString(skillSeparator)
		} else {
			b.WriteString("\n\n")
		}
		b.WriteString(buildLazyPreamble(lazySkills))
	}

	return b.String()
}

// buildLazyPreamble returns the operator-injected text that lists every lazy
// skill by name + description.
func buildLazyPreamble(lazySkills []v1alpha1.KapeSkill) string {
	var b strings.Builder
	b.WriteString("Available skills (call load_skill with the skill name to retrieve full instructions):\n")
	for _, s := range lazySkills {
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Spec.Description)
		b.WriteString("\n")
	}
	b.WriteString("\nWhen you determine a skill is relevant, call load_skill with its name before proceeding.")
	return b.String()
}
