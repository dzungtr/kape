#!/usr/bin/env bash
# migrate-to-github.sh
# One-time migration: create GitHub Milestones and Issues from the kape-io roadmap.
# Run from the repo root: bash tools/migrate-to-github.sh
set -euo pipefail

REPO="dzungtr/kape"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

gh_api() {
  gh api "$@"
}

# Derive area/* label from issue title keywords. Returns empty if ambiguous.
# Mapping mirrors docs/agent-rituals.md → "Title-keyword → area/* mapping".
derive_area() {
  local title="$1"
  local t
  t="$(echo "$title" | tr '[:upper:]' '[:lower:]')"
  case "$t" in
    *"operator"*|*"reconciler"*|*"kapehandler"*|*"kapetool"*|*"kapeschema"*|*"crd validation"*) echo "area/operator" ;;
    *"task-service"*|*"task service"*|*"openapi"*) echo "area/task-service" ;;
    *"adapter"*|*"alertmanager"*|*"audit adapter"*) echo "area/adapters" ;;
    *"kapeproxy"*|*"mcp proxy"*) echo "area/kapeproxy" ;;
    *"runtime"*|*"langgraph"*|*"python"*) echo "area/runtime" ;;
    *"dashboard"*|*"sse"*|*"eventsource"*|*"oauth2 proxy"*) echo "area/dashboard" ;;
    *"helm"*|*"chart"*|*"template"*) echo "area/helm" ;;
    *"crd"*|*"cel"*|*"xvalidation"*) echo "area/crds" ;;
    *"docs"*|*"readme"*|*"runbook"*|*"changelog"*) echo "area/docs" ;;
    *"ci"*|*"workflow"*|*"actions"*|*"snyk"*) echo "area/ci" ;;
    *"nats"*|*"postgres"*|*"cloudnativepg"*|*"cert-manager"*|*"eso"*|*"external secrets"*) echo "area/infra" ;;
    *) echo "" ;;
  esac
}

# Derive phase/Mx-* label from "[Pn/mm]" title prefix.
# Phase → milestone mapping comes from the milestones table.
derive_phase() {
  local title="$1"
  case "$title" in
    \[P6/*) echo "phase/M2-operator" ;;
    \[P7/*) echo "phase/M3-runtime" ;;
    \[P8/*) echo "phase/M4-security" ;;
    \[P9/*) echo "phase/M5-dashboard" ;;
    \[P10/*) echo "phase/M6-release" ;;
    *) echo "" ;;
  esac
}

create_label_if_missing() {
  local name="$1" color="$2"
  if ! gh_api "repos/${REPO}/labels/${name}" &>/dev/null; then
    echo "  Creating label '${name}'..."
    gh_api "repos/${REPO}/labels" \
      -f name="${name}" \
      -f color="${color}" \
      -f description="Roadmap sync issue" \
      --silent
  else
    echo "  Label '${name}' already exists."
  fi
}

create_milestone() {
  local title="$1" state="$2"
  # Check if milestone already exists
  local existing
  existing=$(gh_api "repos/${REPO}/milestones?state=all&per_page=100" \
    --jq ".[] | select(.title == \"${title}\") | .number" 2>/dev/null || true)
  if [[ -n "$existing" ]]; then
    echo "  Milestone '${title}' already exists (#${existing}), skipping."
    echo "$existing"
    return
  fi
  echo "  Creating milestone '${title}' (state=${state})..."
  local ms_number
  ms_number=$(gh_api "repos/${REPO}/milestones" \
    -f title="${title}" \
    -f state="${state}" \
    --jq '.number')
  echo "    -> Milestone #${ms_number}"
  echo "$ms_number"
}

create_issue() {
  local title="$1" body="$2" milestone_number="$3" state="$4"
  # Check if issue already exists with same title
  local existing
  existing=$(gh_api "repos/${REPO}/issues?state=all&per_page=100&labels=roadmap-sync" \
    --jq ".[] | select(.title == \"${title}\") | .number" 2>/dev/null | head -1 || true)
  if [[ -n "$existing" ]]; then
    echo "    Issue '${title}' already exists (#${existing}), skipping."
    return
  fi
  echo "    Creating issue '${title}'..."
  local area_label phase_label
  area_label="$(derive_area "$title")"
  phase_label="$(derive_phase "$title")"

  local label_args=(-f "labels[]=roadmap-sync" -f "labels[]=enhancement" -f "labels[]=committed")
  [[ -n "$area_label" ]]  && label_args+=(-f "labels[]=$area_label")
  [[ -n "$phase_label" ]] && label_args+=(-f "labels[]=$phase_label")
  [[ -z "$area_label" || -z "$phase_label" ]] && label_args+=(-f "labels[]=needs-triage")

  local issue_number
  issue_number=$(gh_api "repos/${REPO}/issues" \
    -f title="${title}" \
    -f body="${body}" \
    -f milestone="${milestone_number}" \
    "${label_args[@]}" \
    --jq '.number')
  echo "      -> Issue #${issue_number}"
  # Close if phase is done
  if [[ "$state" == "closed" ]]; then
    gh_api "repos/${REPO}/issues/${issue_number}" \
      -X PATCH -f state=closed --silent
    echo "      -> Closed (phase done)"
  fi
}

assign_issue_to_milestone() {
  local issue_number="$1" milestone_number="$2"
  echo "    Assigning issue #${issue_number} to milestone #${milestone_number}..."
  gh_api "repos/${REPO}/issues/${issue_number}" \
    -X PATCH -f milestone="${milestone_number}" --silent
}

# ---------------------------------------------------------------------------
# Step 1: Ensure roadmap-sync label exists
# ---------------------------------------------------------------------------
echo "=== Step 1: Ensuring roadmap-sync label ==="
create_label_if_missing "roadmap-sync" "0075ca"

# ---------------------------------------------------------------------------
# Step 2: Create Milestones
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 2: Creating Milestones ==="

echo "Creating M1: Phase 5 — AlertManager Adapter (closed, phase done)..."
M1=$(create_milestone "M1: Phase 5 — AlertManager Adapter" "closed")

echo "Creating M2: Phase 6 — Full Operator (open, in_progress)..."
M2=$(create_milestone "M2: Phase 6 — Full Operator" "open")

echo "Creating M3: Phase 7 — Full Runtime (open, pending)..."
M3=$(create_milestone "M3: Phase 7 — Full Runtime" "open")

echo "Creating M4: Phase 8 — K8s Audit + Security (open, pending)..."
M4=$(create_milestone "M4: Phase 8 — K8s Audit + Security" "open")

echo "Creating M5: Phase 9 — Dashboard (open, pending)..."
M5=$(create_milestone "M5: Phase 9 — Dashboard" "open")

echo "Creating M6: Phase 10 — Helm + Examples + Polish (open, pending)..."
M6=$(create_milestone "M6: Phase 10 — Helm + Examples + Polish" "open")

echo ""
echo "Milestones: M1=#${M1} M2=#${M2} M3=#${M3} M4=#${M4} M5=#${M5} M6=#${M6}"

# ---------------------------------------------------------------------------
# Step 3: Create Issues — Phase 5 (M1, done → closed)
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 3: Creating Issues for Phase 5 (M1, done) ==="
# Phase 5 has no plans/ subdirectory with slice files — it's a single-slice phase.
# We create one issue representing the full phase deliverable.

create_issue \
  "[P5/alertmanager-adapter] AlertManager Adapter — HTTP webhook to CloudEvent + NATS" \
  "**Phase:** 5 — AlertManager Adapter
**Milestone:** M1: Phase 5 — AlertManager Adapter
**Status:** done

## Description

HTTP server (Chi) at \`adapters/kape-alertmanager-adapter/\`. Accepts AlertManager webhook POSTs, normalises them to CloudEvents (\`type = kape.events.alertmanager\`), and publishes to NATS JetStream. Includes Prometheus metrics (events received/published/errors) and NATS \`KAPE_EVENTS\` stream setup.

**M1 gate:** Task record exists in DB with \`status: completed\` and populated \`schema_output\` driven by a real AlertManager alert.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M1" \
  "closed"

# ---------------------------------------------------------------------------
# Step 4: Create Issues — Phase 6 (M2, in_progress → open)
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 4: Creating Issues for Phase 6 (M2, in_progress) ==="

create_issue \
  "[P6/slice-1] KapeSkill CRD + KapeSkillReconciler" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Introduce the \`KapeSkill\` CRD (types, deepcopy, generated CRD manifest), a \`SkillRepository\` port + adapter, and the \`KapeSkillReconciler\` — covering both the happy path (Ready KapeTool → skill Ready) and deletion protection (finalizer blocks delete while any KapeHandler references the skill).

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

create_issue \
  "[P6/slice-2] KapeSchema event recording" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Emit \`DeletionBlocked\` (Warning) and \`SchemaValid\` (Normal) Kubernetes events from the KapeSchema reconciler, which currently only writes Conditions and records no events.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

create_issue \
  "[P6/slice-3] KapeHandlerReconciler skill gate + tool union + system prompt + lazy ConfigMap" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Extend \`KapeHandlerReconciler\` to gate on KapeSkill readiness, union skill-pulled tools with handler-direct tools into a deduplicated toolMap, assemble the full system prompt (handler + eager skill instructions + lazy skill preamble) into \`settings.toml\`, reconcile a \`kape-skills-{handler-name}\` ConfigMap when lazy skills exist, extend \`computeRolloutHash\` to cover sorted tool Specs + ordered skill Specs, extend label sync with skill-ref and tool-ref labels, and flip the Ready rollup to be forward-compatible.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

create_issue \
  "[P6/slice-4] KapeSkill cross-resource watch wiring" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Wire a secondary watch so that any change to a \`KapeSkill\` resource automatically re-enqueues every \`KapeHandler\` that references it, verified by observing the rollout-hash annotation change on the referencing handler's Deployment within one reconcile cycle.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

create_issue \
  "[P6/slice-5] kapeproxy-config rendering + sidecar injection" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Replace per-tool \`kapetool-*\` sidecar containers with a single \`kapeproxy\` sidecar that reads a rendered \`kapeproxy-config-{handler-name}\` ConfigMap, and ship a stub binary that satisfies slice 5 acceptance criteria.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

create_issue \
  "[P6/slice-6] KapeProxyReconciler" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Add a \`KapeProxyReconciler\` that watches Pods with label \`kape.io/handler=*\`, classifies the kapeproxy container's health, and writes \`KapeProxyReady\` / \`KapeProxyDegraded\` conditions on the parent \`KapeHandler\` — with no writes to any other resource.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

create_issue \
  "[P6/slice-7] kapeproxy production binary" \
  "**Phase:** 6 — Full Operator
**Milestone:** M2: Phase 6 — Full Operator
**Status:** in_progress

## Description

Replace the slice-5 stub with a production \`kapeproxy\` binary that parses the rendered \`kapeproxy-config-{handler-name}\` ConfigMap, fans tool-list and tool-call requests out to multiple upstream MCP servers (with namespaced names, allowlist filtering, JSONPath input/output redaction, audit logging, single-span OTEL tracing, and W3C TraceContext propagation).

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M2" \
  "open"

# ---------------------------------------------------------------------------
# Step 5: Create Issues — Phase 7 (M3, pending → open)
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 5: Creating Issues for Phase 7 (M3, pending) ==="

create_issue \
  "[P7/01] Proxy config — settings.toml [proxy] section + kapeproxy MCPToolkit" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

Replace per-tool \`[tools.*]\` sections in \`settings.toml\` with a single \`[proxy]\` section pointing at kapeproxy. Update \`config.py\` to read \`[proxy]\` instead of per-tool sidecar port config.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

create_issue \
  "[P7/02] kapeproxy MCP integration — full LangGraph ReAct loop via single federation endpoint" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

Connect runtime to kapeproxy via single \`MCPToolkit\` connection. Add \`call_tools\` node (LangGraph \`ToolNode\`) to graph. Full graph: \`entry_router → reason ⇄ call_tools → respond\`.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

create_issue \
  "[P7/03] load_skill tool — lazy skill instruction loader for LangGraph" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

Always-registered \`load_skill\` LangGraph tool that reads from \`/etc/kape/skills/{skill_name}.txt\` with Jinja2 rendering using the standard event context. Gracefully returns not-found message if dir/file absent.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

create_issue \
  "[P7/04] Memory tool — Qdrant vector store retriever integration" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

Connect to Qdrant via \`QDRANT_URL\` + \`QDRANT_COLLECTION\` env vars. Build LangChain \`QdrantVectorStore\` retriever. Register as LangChain tool in LangGraph tool registry.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

create_issue \
  "[P7/05] ActionsRouter — JSONPath conditions + event-publish + webhook + save_memory" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

JSONPath condition evaluation against \`schema_output\`. Actions: \`event-publish\` (Jinja2 + CloudEvent to NATS), \`webhook\` (HTTP POST), \`save_memory\` (Qdrant upsert). All actions run; partial failures logged, do not fail the Task.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

create_issue \
  "[P7/06] Retry + DLQ — tenacity backoff + dead-letter queue for failed tasks" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

Wrap LangGraph invoke with \`tenacity\` exponential backoff (LLM 429, 503). Non-retryable errors → immediate DLQ. DLQ: publish to \`kape.events.dlq.<handler-name>\` with original CloudEvent + error context.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

create_issue \
  "[P7/07] Dedup sliding window + Prometheus metrics" \
  "**Phase:** 7 — Full Runtime
**Milestone:** M3: Phase 7 — Full Runtime
**Status:** pending

## Description

In-memory dedup set of CloudEvent IDs with 60s TTL. Prometheus metrics via \`prometheus_client\`: \`kape_events_total\`, \`kape_llm_latency_seconds\`, \`kape_tool_calls_total\`, \`kape_decisions_total\` — all labelled by handler name.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M3" \
  "open"

# ---------------------------------------------------------------------------
# Step 6: Create Issues — Phase 8 (M4, pending → open)
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 6: Creating Issues for Phase 8 (M4, pending) ==="

create_issue \
  "[P8/01] K8s Audit Adapter — CloudEvent publisher for K8s API server audit events" \
  "**Phase:** 8 — K8s Audit + Security
**Milestone:** M4: Phase 8 — K8s Audit + Security
**Status:** pending

## Description

HTTP server accepting K8s API server audit webhook events (\`adapters/kape-audit-adapter/\`). Audit policy: verbs \`create\`, \`update\`, \`delete\` on Secrets, RoleBindings, ClusterRoleBindings, Pods. Publishes to \`kape.events.audit.<verb>.<resource>\`.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M4" \
  "open"

create_issue \
  "[P8/02] Network policy manifests — handler pod egress restrictions" \
  "**Phase:** 8 — K8s Audit + Security
**Milestone:** M4: Phase 8 — K8s Audit + Security
**Status:** pending

## Description

Handler pod egress: NATS (4222), LLM provider (443), MCP sidecars (by pod label), task-service (8080), Qdrant (6333). All other egress denied. Standard NetworkPolicy + Cilium variant in \`examples/networkpolicy/\`.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M4" \
  "open"

create_issue \
  "[P8/03] Prompt injection defense — HTML-escape + XML tag isolation in system prompt" \
  "**Phase:** 8 — K8s Audit + Security
**Milestone:** M4: Phase 8 — K8s Audit + Security
**Status:** pending

## Description

Full system prompt template in \`runtime/graph/system_prompt.j2\`. HTML-escape all \`event_raw\` fields before Jinja2 rendering. XML tag isolation: wrap user content in \`<event>...</event>\` tags.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M4" \
  "open"

create_issue \
  "[P8/04] Secret management — ESO SecretStore + ExternalSecret examples + file mounts" \
  "**Phase:** 8 — K8s Audit + Security
**Milestone:** M4: Phase 8 — K8s Audit + Security
**Status:** pending

## Description

ESO \`SecretStore\` + \`ExternalSecret\` example manifests (\`examples/eso/\`). Operator mounts KapeTool connection Secrets as files (not env vars).

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M4" \
  "open"

create_issue \
  "[P8/05] Immutable audit log — PostgreSQL INSERT-only trigger on terminal-state rows" \
  "**Phase:** 8 — K8s Audit + Security
**Milestone:** M4: Phase 8 — K8s Audit + Security
**Status:** pending

## Description

PostgreSQL role \`kape_writer\` with \`INSERT\` only — no \`UPDATE\` on terminal-state rows via trigger.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M4" \
  "open"

create_issue \
  "[P8/06] mTLS for NATS — cert-manager certificates for cluster + clients" \
  "**Phase:** 8 — K8s Audit + Security
**Milestone:** M4: Phase 8 — K8s Audit + Security
**Status:** pending

## Description

cert-manager \`Certificate\` resources for NATS cluster + all client connections. Update NATS StatefulSet and all publisher/consumer configs.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M4" \
  "open"

# ---------------------------------------------------------------------------
# Step 7: Create Issues — Phase 9 (M5, pending → open)
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 7: Creating Issues for Phase 9 (M5, pending) ==="

create_issue \
  "[P9/01] TypeScript type generation from OpenAPI spec" \
  "**Phase:** 9 — Dashboard
**Milestone:** M5: Phase 9 — Dashboard
**Status:** pending

## Description

Generate TypeScript types from \`task-service/openapi/openapi.yaml\` → \`dashboard/app/types/api.ts\`.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M5" \
  "open"

create_issue \
  "[P9/02] Task list + SSE — live task feed with EventSource and infinite scroll" \
  "**Phase:** 9 — Dashboard
**Milestone:** M5: Phase 9 — Dashboard
**Status:** pending

## Description

\`tasks._index.tsx\` — live task feed, SSE-connected, infinite scroll. \`EventSource\` connecting to \`GET /tasks/stream\`.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M5" \
  "open"

create_issue \
  "[P9/03] Task detail — event payload, LLM decision, action timeline, trace link" \
  "**Phase:** 9 — Dashboard
**Milestone:** M5: Phase 9 — Dashboard
**Status:** pending

## Description

\`tasks.\$id.tsx\` — task detail: event payload, LLM decision, action timeline, Arize trace link.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M5" \
  "open"

create_issue \
  "[P9/04] Handler routes — health overview + per-handler detail page" \
  "**Phase:** 9 — Dashboard
**Milestone:** M5: Phase 9 — Dashboard
**Status:** pending

## Description

\`handlers._index.tsx\` — handler health overview: replica count, events/min, p99 latency, decision distribution. \`handlers.\$name.tsx\` — handler detail: recent tasks, decision distribution, DLQ count.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M5" \
  "open"

create_issue \
  "[P9/05] Auth + Deploy — OAuth2 Proxy GitHub org check + Kubernetes deployment" \
  "**Phase:** 9 — Dashboard
**Milestone:** M5: Phase 9 — Dashboard
**Status:** pending

## Description

OAuth2 Proxy: GitHub org/team membership check. Deployment: \`kape-system\` namespace, ClusterIP Service, Ingress with TLS termination. Dockerfile: \`node:22-alpine\`, server-side rendering.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M5" \
  "open"

# ---------------------------------------------------------------------------
# Step 8: Create Issues — Phase 10 (M6, pending → open)
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 8: Creating Issues for Phase 10 (M6, pending) ==="

create_issue \
  "[P10/01] Helm infrastructure templates — NATS, CloudNativePG, cert-manager, CRDs" \
  "**Phase:** 10 — Helm + Examples + Polish
**Milestone:** M6: Phase 10 — Helm + Examples + Polish
**Status:** pending

## Description

Helm templates for: NATS JetStream StatefulSet, CloudNativePG cluster, CRDs, cert-manager Issuer + Certificate. \`helm/values.yaml\` with all configurable defaults.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M6" \
  "open"

create_issue \
  "[P10/02] Helm application templates — operator, task-service, adapters, dashboard, OAuth2 Proxy" \
  "**Phase:** 10 — Helm + Examples + Polish
**Milestone:** M6: Phase 10 — Helm + Examples + Polish
**Status:** pending

## Description

Helm templates for: KAPE operator, task-service, AlertManager adapter, K8s Audit adapter, Dashboard, OAuth2 Proxy. \`helm/values.yaml\`: image tags, resource requests, LLM provider, NATS retention, replicas.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M6" \
  "open"

create_issue \
  "[P10/03] Examples — alertmanager-handler + audit-handler complete manifests" \
  "**Phase:** 10 — Helm + Examples + Polish
**Milestone:** M6: Phase 10 — Helm + Examples + Polish
**Status:** pending

## Description

\`examples/alertmanager-handler/\`: complete KapeHandler + KapeTool + KapeSchema + KapeSkill for AlertManager use case. \`examples/audit-handler/\`: complete example for K8s Audit use case.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M6" \
  "open"

create_issue \
  "[P10/04] Docs + Release — DEMO.md runbook + 0.1.0 image tagging + CHANGELOG" \
  "**Phase:** 10 — Helm + Examples + Polish
**Milestone:** M6: Phase 10 — Helm + Examples + Polish
**Status:** pending

## Description

\`DEMO.md\`: step-by-step runbook — install Helm chart, apply examples, fire test alert, watch task in dashboard. Image tagging: all components tagged \`0.1.0\` in \`values.yaml\`. \`npx changeset\` → \`CHANGELOG.md\` per component → \`0.1.0\` tag.

**M6 gate:** Full install → real event → visible decision. v1 Release Candidate tagged.

*This issue was created by the roadmap→GitHub migration script.*" \
  "$M6" \
  "open"

# ---------------------------------------------------------------------------
# Step 9: Assign existing debt/follow-up issues to milestones
# ---------------------------------------------------------------------------
echo ""
echo "=== Step 9: Assigning existing debt issues to milestones ==="

# Issue 13: KapeTool memory: provision via database-operator CRDs
# Clearly about KapeTool memory provisioning — Phase 6 / M2 concern
echo "  #13 — KapeTool memory provisioning → M2"
assign_issue_to_milestone 13 "$M2"

# Issue 14: KapeSchema CRD: hand-added CEL XValidation rules drift
# KapeSchema CRD — Phase 6 / M2 (KapeSchema reconciler work)
echo "  #14 — KapeSchema CEL XValidation drift → M2"
assign_issue_to_milestone 14 "$M2"

# Issue 26: Move KAPE_EVENTS JetStream stream provisioning out of publisher into infra
# NATS infra concern — could be Phase 7 (runtime) but foundational; assign to M3
echo "  #26 — KAPE_EVENTS stream provisioning → M3"
assign_issue_to_milestone 26 "$M3"

# Issue 29: Replace kape_decisions_total with kape_action_results_total
# Prometheus metrics — Phase 7 / M3 (metrics slice)
echo "  #29 — Prometheus metric rename → M3"
assign_issue_to_milestone 29 "$M3"

# Issue 31: Design gap: no embedding model config for memory tools
# Memory tool config — Phase 7 / M3 (memory tool integration)
echo "  #31 — Embedding model config gap → M3"
assign_issue_to_milestone 31 "$M3"

# Issue 32: Design gap: no structured Secret reference for LLM API key
# LLM API key injection — Phase 6 / M2 (operator handles secret injection)
echo "  #32 — LLM API key Secret reference → M2"
assign_issue_to_milestone 32 "$M2"

# Issue 34: add envtest integration test suite
# Cross-cutting, most relevant to Phase 6 / M2 (operator work)
echo "  #34 — envtest integration tests → M2"
assign_issue_to_milestone 34 "$M2"

# Issue 37: webhook admission for KAPE CRDs (deferred from M2)
# Explicitly deferred from M2 — assign to M2
echo "  #37 — webhook admission (deferred from M2) → M2"
assign_issue_to_milestone 37 "$M2"

# Issue 38: KapeTool memory-type deletion protection (deferred from M2)
# Explicitly deferred from M2 — assign to M2
echo "  #38 — KapeTool deletion protection (deferred from M2) → M2"
assign_issue_to_milestone 38 "$M2"

# Issue 51: decompose KapeHandlerReconciler (complexity after slices 3-4)
# Phase 6 refactor — M2
echo "  #51 — KapeHandlerReconciler decompose → M2"
assign_issue_to_milestone 51 "$M2"

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
echo ""
echo "=== Migration complete ==="
echo ""
echo "Summary:"
echo "  Milestones created: M1=#${M1} M2=#${M2} M3=#${M3} M4=#${M4} M5=#${M5} M6=#${M6}"
echo "  Issues created for phases 5, 6, 7, 8, 9, 10"
echo "  Existing issues #13,14,26,29,31,32,34,37,38,51 assigned to milestones"
echo ""
echo "GitHub is now the source of truth. The docs/roadmap/ files are reference-only."
