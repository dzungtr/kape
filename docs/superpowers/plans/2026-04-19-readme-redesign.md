# README Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the verbose reference README with a concise, vision-first README in SigNoz style.

**Architecture:** Single file replacement — `README.md` at repo root. No other files touched.

**Tech Stack:** Markdown only.

---

### Task 1: Replace README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Overwrite README.md with the new content**

Replace the entire contents of `/home/tony/projects/kape-io/README.md` with:

```markdown
# KAPE — Kubernetes Agentic Platform Execution

> Autonomous cluster monitoring, reasoning, and remediation — declared entirely in Kubernetes CRDs.

![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Go](https://img.shields.io/badge/go-1.23-00ADD8)
![Python](https://img.shields.io/badge/python-3.12-3776AB)

## Why KAPE?

Kubernetes clusters generate constant signal — security violations, resource pressure, policy breaches, cost anomalies.
Existing tools either **act without reasoning** (autoscalers, Kyverno) or **reason without acting** (K8sGPT).
KAPE does both.

- **Event-driven** — ingests signals from Falco, Cilium, Kyverno, Karpenter, and K8s Audit via CloudEvents
- **LLM-powered reasoning** — LangGraph ReAct agents analyse context and produce structured decisions
- **Declarative config** — define agent behaviour entirely through `KapeHandler`, `KapeTool`, and `KapeSchema` CRDs — no code changes required
- **Scales to zero** — KEDA drives handler pods from NATS consumer lag; idle agents cost nothing

## Quick Start

```bash
helm install kape ./helm/kape --namespace kape-system --create-namespace
```

Then declare your first agent:

```yaml
apiVersion: kape.io/v1alpha1
kind: KapeHandler
metadata:
  name: falco-responder
spec:
  source: falco
  llm:
    model: gpt-4o
  tools:
    - name: kubectl-readonly
  schemaRef: incident-triage-v1
```

→ [Full getting-started guide](docs/)

## Documentation

| | |
|---|---|
| [Architecture](docs/architecture.md) | How the platform fits together |
| [CRD Reference](docs/crds.md) | KapeHandler, KapeTool, KapeSchema fields |
| [Design Specs](specs/) | RFCs and detailed technical decisions |
| [Contributing](CONTRIBUTING.md) | Development setup and workflow |

## Status

Early development — core operator, runtime, and adapters are functional. Dashboard and task-service in progress.
```

- [ ] **Step 2: Verify the file looks correct**

```bash
cat README.md
```

Expected: clean markdown, ~60 lines, no old content remaining.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "doc: rewrite README — concise vision-first format"
```

- [ ] **Step 4: Push branch and open PR**

```bash
git push -u origin HEAD
gh pr create --title "doc: rewrite README — concise vision-first format" --body "$(cat <<'EOF'
## Summary

- Replaces verbose reference README with a concise SigNoz-style README
- Leads with a differentiator pitch (vs autoscalers / K8sGPT)
- Adds quick-start helm + KapeHandler YAML snippet
- Adds docs navigation table
- Honest one-line status at the bottom

## Test plan

- [ ] Render README on GitHub and verify formatting
- [ ] Check all section headings are present: Why KAPE, Quick Start, Documentation, Status

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
