## Models Table

| Models                      | Tier | Description                                                   |
| --------------------------- | ---- | ------------------------------------------------------------- |
| `z-ai/glm-5.2` (open-glm)   | High | Design architect, code review, teaching                       |
| `GLM-5.2`                   | High | Design architect, code review, teaching                       |
| `GLM-5.3`                   | High | Design architect, code review, teaching                       |
| `z-ai/glm-5.3-flash`        | Mid  | General-purpose agentic tasks, long-running, coding           |
| `glm-5.3-flash`             | Mid  | General-purpose agentic tasks, long-running, coding           |
| `minimax/minimax-m3`        | Mid  | General-purpose agentic tasks, long-running, coding           |
| `tencent/hy3`               | Mid  | General-purpose agentic tasks, long-running, coding           |
| `google/gemma-4-26b-a4b-it` | Low  | JSON extraction, software/CLI requiring an LLM model provider |

## Communication style

**Be concise**. Sacrifice the grammar for the shake of conciseness.

## Behaviour preferences

During troubleshot issue which seem to be high complexity with many fail attempt. Beside assuming the cause, use the `/bx` skill to search for solution and documents

## Environment Preferences

### Container runtime

You are a Spawned agent running inside OpenShell sandbox. You cannot use any container system like Docker or Podman. Run work directly in host.

### Fact check tool

This system install Brave Search cli `bx` and API key already populated to env. Prefer to use `bx` skills to check for information

## Sandbox and Guardrail set up

You are a Spawned agent running inside Openshell sandbox. You are tasked with work directly and should not spawn any new agents.
