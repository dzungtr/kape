---
title: README Redesign
date: 2026-04-19
status: approved
---

# README Redesign Spec

## Goal

Replace the current detailed-reference README with a professional, concise README in the style of SigNoz — vision-first, audience: both external GitHub visitors and internal team.

## Audience

Both engineers browsing GitHub who might use/contribute to KAPE, and internal collaborators who already know the project.

## Tone & Style

SigNoz-style: short hero pitch, "why us" differentiator bullets, quick-start code block, docs navigation links, honest status line at the bottom.

## Structure

### 1. Hero Block (~10 lines)

- `# KAPE — Kubernetes Agentic Platform Execution` heading
- One-line tagline: _Autonomous cluster monitoring, reasoning, and remediation — declared entirely in Kubernetes CRDs._
- Three inline badges: License (Apache 2.0), Go version, Python version

### 2. Why KAPE? (~8 lines)

- Two-sentence framing: existing tools either act without reasoning (autoscalers, Kyverno) or reason without acting (K8sGPT); KAPE does both.
- Four differentiator bullets, each bold-keyed:
  - **Event-driven** — Falco, Cilium, Kyverno, Karpenter, K8s Audit via CloudEvents
  - **LLM-powered reasoning** — LangGraph ReAct agents, structured decisions
  - **Declarative config** — KapeHandler, KapeTool, KapeSchema CRDs; no code changes
  - **Scales to zero** — KEDA on NATS consumer lag

### 3. Quick Start (~15 lines)

- `helm install` one-liner
- Minimal `KapeHandler` YAML snippet showing the real API shape
- Link to full getting-started guide in `docs/`

### 4. Documentation Table (~8 lines)

Two-column table linking to:
- Architecture
- CRD Reference
- Design Specs (`specs/`)
- Contributing

### 5. Status (~2 lines)

One honest sentence: core operator, runtime, and adapters are functional; dashboard and task-service in progress.

## What Is Removed

- Verbose "How It Works" numbered list
- Full implementation stack table
- Full CRD responsibility table
- Design specs table (moved to Documentation section as a single link)
- Security model section (moves to dedicated doc)

## Constraints

- No logo or screenshot placeholder (not yet available)
- Badge URLs to be filled in once repo is public / CI is configured
- `docs/architecture.md`, `docs/crds.md`, `CONTRIBUTING.md` are linked but not yet written — that is acceptable; links serve as signposts
