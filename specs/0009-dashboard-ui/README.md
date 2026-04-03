# KAPE Dashboard — Technical Design

**Status:** Draft
**Author:** Dzung Tran
**Session:** 9 — UI Dashboard Design
**Created:** 2026-04-03
**Depends on:** `kape-rfc.md`, `kape-audit-design.md`, `kape-handler-runtime-design.md`

---

## Table of Contents

1. [Overview](#1-overview)
2. [Technology Stack](#2-technology-stack)
3. [Authentication](#3-authentication)
4. [Architecture](#4-architecture)
5. [Route Map](#5-route-map)
6. [Page Designs](#6-page-designs)
   - 6.1 [Tasks — Live Feed](#61-tasks--live-feed)
   - 6.2 [Task Detail](#62-task-detail)
   - 6.3 [Retry Lineage Chain](#63-retry-lineage-chain)
   - 6.4 [Handlers](#64-handlers)
   - 6.5 [Handler Detail](#65-handler-detail)
7. [Real-Time Updates](#7-real-time-updates)
8. [API Surface](#8-api-surface)
9. [Deployment](#9-deployment)
10. [Observability Boundary](#10-observability-boundary)
11. [Future Work](#11-future-work)

---

## 1. Overview

The KAPE Dashboard is the primary operational interface for engineers managing KAPE handler execution. It surfaces Task lifecycle records from `kape-task-service` and provides the management operations defined in the RFC — timeout marking and retry initiation.

**The dashboard is not read-only.** It owns event lifecycle management for the two write operations in v1:

- **Timeout** — marking a stuck `Processing` task as `Timeout` (single or bulk)
- **Retry** — re-publishing a failed task's original event to NATS

All reads and writes are mediated by `kape-task-service`. The dashboard never connects to PostgreSQL directly.

**Design principles:**

- Task lifecycle management, not metrics. Handler throughput, latency p99, and failure rates are owned by Prometheus and surfaced via Grafana — not replicated here.
- Auth at the dashboard boundary. `kape-task-service` is cluster-internal and trusts the dashboard. Authentication is enforced entirely in the dashboard server process.
- No Kubernetes API calls from the dashboard. Handler metadata (namespace, last activity) is derived from task records, not from CRD reads.
- Any authenticated user has full access. Role-based access control is deferred to v2.

---

## 2. Technology Stack

### 2.1 Framework: React Router v7 (Framework Mode)

React Router v7 in framework mode is a server-side React application. Every request is handled by a Node.js server process. Route loaders run server-side — data fetching, session validation, and `kape-task-service` proxying all happen on the server before the response reaches the browser.

This resolves both the backend question and the CORS question simultaneously:

```
Browser
  → React Router v7 Node.js server  (kape-dashboard Pod in kape-system)
      loaders  → kape-task-service over cluster DNS (no CORS, no token exposure)
      actions  → kape-task-service write endpoints
      SSE      → proxied task stream from kape-task-service
  ← HTML + minimal JS hydration
```

There is no separate backend service. The React Router server is the backend. One Deployment, one container, one `node server.js`.

**Why not Next.js:** Next.js is Vercel-optimised. KAPE deploys inside `kape-system` on arbitrary Kubernetes clusters — Vercel infrastructure assumptions are actively hostile in this context. React Router v7 is deployment-agnostic by design and runs on a plain Node.js server with no platform-specific configuration.

**Why not Astro:** Astro is explicitly not designed for dashboards or real-time applications. The live Task feed, SSE updates, and interactive mutation flows fight Astro's islands architecture.

### 2.2 Language: TypeScript

Strict TypeScript throughout. Types for `Task`, `TaskStatus`, `ActionResult`, and `TaskError` are generated from the `kape-task-service` OpenAPI spec and shared across loaders, actions, and components.

### 2.3 Key Dependencies

| Package             | Purpose                                             |
| ------------------- | --------------------------------------------------- |
| `react-router`      | Framework (loaders, actions, SSE resource routes)   |
| `remix-utils`       | `eventStream` and `useEventSource` SSE helpers      |
| `@radix-ui/react-*` | Accessible UI primitives (modal, dropdown, tooltip) |
| `tailwindcss`       | Utility CSS                                         |

The dashboard has no authentication dependencies. Auth is handled entirely by OAuth2 Proxy — see Section 3.

---

## 3. Authentication

### 3.1 Model: OAuth2 Proxy + GitHub

Authentication is handled entirely by **OAuth2 Proxy** — a separate Deployment in `kape-system` that sits between the Ingress and the dashboard. The dashboard itself contains zero authentication code. It is auth-unaware and receives only pre-authenticated requests.

**Provider:** GitHub OAuth App. OAuth2 Proxy's native GitHub provider is used — no generic OIDC configuration required.

**Access restriction:** GitHub team membership. Only members of a configured GitHub organisation team can access the dashboard. Unauthenticated requests and requests from users outside the team receive a `403` from OAuth2 Proxy before reaching the dashboard.

Any team member who passes authentication has full access to all dashboard capabilities, including Timeout and Retry operations. Role-based access control is deferred to v2 — see [Section 11](#11-future-work).

### 3.2 Authentication Flow

```
1. User visits dashboard — no session cookie present
2. Ingress routes request to OAuth2 Proxy
3. OAuth2 Proxy redirects to GitHub OAuth authorization endpoint
4. User authenticates with GitHub and authorises the OAuth App
5. GitHub redirects to OAuth2 Proxy callback with authorization code
6. OAuth2 Proxy exchanges code for access token, verifies team membership
7. OAuth2 Proxy creates encrypted session cookie and redirects to original URL
8. Subsequent requests: OAuth2 Proxy validates session cookie, injects headers,
   forwards to kape-dashboard:
     X-Forwarded-User:  github-username
     X-Forwarded-Email: user@example.com
9. kape-dashboard reads X-Forwarded-User for display only — no auth logic
```

Session management (cookie encryption, expiry, refresh) is owned entirely by OAuth2 Proxy. The dashboard has no session state.

### 3.3 Helm Chart Configuration

```yaml
auth:
  github:
    clientId: "your-github-oauth-app-client-id"
    clientSecretRef:
      name: kape-dashboard-github-oauth
      key: client_secret
    org: "your-github-org" # GitHub organisation name
    team: "platform-team" # GitHub team within the org
    cookieSecretRef:
      name: kape-dashboard-oauth2-proxy
      key: cookie_secret # 32-byte random value for cookie encryption
```

### 3.4 `kape-task-service` Trust Model

`kape-task-service` does not implement its own authentication. It is a cluster-internal API reachable only within `kape-system`. A Kubernetes `NetworkPolicy` restricts ingress to `kape-task-service` to pods with the `app: kape-dashboard` label. The dashboard is the sole authorised caller of `kape-task-service` endpoints.

Engineers who need direct `kape-task-service` access for debugging must use `kubectl port-forward` — there is no public endpoint.

---

## 4. Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  kape-system                                                        │
│                                                                     │
│  ┌─────────────────────────────┐                                    │
│  │  oauth2-proxy               │                                    │
│  │  Deployment (1 replica)     │                                    │
│  │                             │                                    │
│  │  Provider: GitHub           │                                    │
│  │  Restricts: org/team        │                                    │
│  │  Injects: X-Forwarded-User  │                                    │
│  │           X-Forwarded-Email │                                    │
│  └──────────────┬──────────────┘                                    │
│                 │ authenticated requests only                        │
│  ┌──────────────▼──────────────┐                                    │
│  │  kape-dashboard             │                                    │
│  │  Deployment (1 replica)     │                                    │
│  │  React Router v7 / Node.js  │                                    │
│  │  (zero auth code)           │                                    │
│  │                             │   ┌────────────────────────────┐  │
│  │  Route loaders (GET)    ────┼──▶│  kape-task-service         │  │
│  │  Route actions (POST/PATCH) │   │  :8080                     │  │
│  │  SSE resource route         │   └────────────────────────────┘  │
│  └─────────────────────────────┘                                    │
│                ▲                                                    │
│                │  Ingress (HTTPS)                                   │
└────────────────┼────────────────────────────────────────────────────┘
                 │
             Browser
```

**Request flow:** the Ingress routes all traffic to OAuth2 Proxy first. OAuth2 Proxy validates the session cookie and checks GitHub team membership. Authenticated requests are forwarded to the dashboard with injected identity headers. Unauthenticated or unauthorised requests are redirected to GitHub — the dashboard never sees them.

**Replica count:** 1 for both OAuth2 Proxy and the dashboard in v1. Both are stateless — OAuth2 Proxy stores sessions in encrypted cookies, the dashboard has no session state. Either can scale to multiple replicas without coordination.

---

## 5. Route Map

Auth routes (`/oauth2/callback`, `/oauth2/sign_out`) are owned by OAuth2 Proxy and never reach the dashboard.

| Route                      | Page                                   | Notes |
| -------------------------- | -------------------------------------- | ----- |
| `GET /`                    | Redirect to `/tasks`                   |       |
| `GET /tasks`               | Live Task Feed                         |       |
| `GET /tasks/:id`           | Task Detail                            |       |
| `GET /tasks/:id/lineage`   | Retry Lineage Chain                    |       |
| `GET /handlers`            | Handlers Table                         |       |
| `GET /handlers/:name`      | Handler Detail + Decision Distribution |       |
| `POST /tasks/:id/retry`    | Retry action (proxies to task service) |       |
| `PATCH /tasks/:id/status`  | Single Timeout action                  |       |
| `PATCH /tasks/bulk/status` | Bulk Timeout action                    |       |
| `GET /sse/tasks`           | SSE stream resource route              |       |

---

## 6. Page Designs

### 6.1 Tasks — Live Feed

**URL:** `/tasks`

The primary view. Shows all tasks across all handlers in reverse chronological order, updated in real-time via SSE.

**Filter bar:**

```
[ Handler: All ▾ ]  [ Status: All ▾ ]  [ Last: 1h ▾ ]  [ Search event type... ]    ● Live
```

- **Handler** multi-select: populated from distinct `handler` values in the current time window
- **Status** multi-select: all nine `TaskStatus` values
- **Time range**: Last 15m / 1h / 6h / 24h / Custom
- **Event type search**: free-text prefix filter on `event_type`
- **● Live indicator**: green when SSE is connected. Click to pause — incoming tasks are buffered but not rendered until the operator clicks `Resume`. Prevents rows from jumping while the operator is reading a specific entry.

All active filters are reflected in URL search params (`/tasks?handler=falco-terminal-shell&status=ActionError&since=1h`), making filtered views bookmarkable and shareable.

**Table columns:**

| Column             | Width | Notes                                                                                                                        |
| ------------------ | ----- | ---------------------------------------------------------------------------------------------------------------------------- |
| Checkbox           | 32px  | Selectable only for `Processing` tasks — used for bulk Timeout. Disabled for all other statuses.                             |
| Task ID            | 120px | First 8 chars of ULID, monospace. Click-to-copy full ID.                                                                     |
| Handler            | 180px | Truncated. Clicking the handler name applies it as a filter — does not navigate away.                                        |
| Event Type         | 220px | Truncated from the left — the distinguishing suffix is the most meaningful part. Full value on hover tooltip.                |
| Status             | 140px | Colour-coded badge. See status colours below.                                                                                |
| Elapsed / Duration | 90px  | `Processing`: live ticking counter from `received_at` (e.g. `2m 14s`). Terminal statuses: fixed `duration_ms` (e.g. `1.2s`). |
| Decision           | 120px | `schema_output.decision` if `Completed`. `—` for any status that did not reach `parse_output`.                               |
| Actions            | 90px  | Context-sensitive per status. See below.                                                                                     |

**Status badge colours:**

| Status                   | Colour                                           |
| ------------------------ | ------------------------------------------------ |
| `Processing`             | Blue, pulsing animation                          |
| `Completed`              | Green                                            |
| `ActionError`            | Amber                                            |
| `Failed`                 | Red                                              |
| `SchemaValidationFailed` | Red                                              |
| `UnprocessableEvent`     | Red                                              |
| `Timeout`                | Grey                                             |
| `Retried`                | Grey with strikethrough text                     |
| `PendingApproval`        | Purple (v2 — badge renders, no action available) |

**Row action buttons (rightmost column):**

| Task Status              | Button                                  |
| ------------------------ | --------------------------------------- |
| `Processing`             | `[ Timeout ]`                           |
| `ActionError`            | `[ Retry ]`                             |
| `Failed`                 | `[ Retry ]`                             |
| `SchemaValidationFailed` | `[ Retry ]`                             |
| `Timeout`                | `[ Retry ]`                             |
| `Retried`                | `[ View chain ]` → `/tasks/:id/lineage` |
| `Completed`              | —                                       |
| `UnprocessableEvent`     | —                                       |

**Bulk action bar:**

Appears above the table when one or more checkboxes are selected.

```
3 tasks selected    [ Mark as Timeout ]    [ Clear selection ]
```

`[ Mark as Timeout ]` opens a confirmation modal before firing.

**Confirmation modals:**

Bulk Timeout:

```
┌─────────────────────────────────────────────┐
│  Mark 3 tasks as Timeout?                   │
│                                             │
│  This cannot be undone automatically.       │
│  Timed-out tasks can be retried.            │
│                                             │
│              [ Cancel ]  [ Confirm ]        │
└─────────────────────────────────────────────┘
```

Single Timeout (same pattern, singular copy):

```
┌─────────────────────────────────────────────┐
│  Mark task 01JKXYZ... as Timeout?           │
│                                             │
│  This cannot be undone automatically.       │
│  Timed-out tasks can be retried.            │
│                                             │
│              [ Cancel ]  [ Confirm ]        │
└─────────────────────────────────────────────┘
```

Retry:

```
┌─────────────────────────────────────────────┐
│  Retry task 01JKXYZ...?                     │
│                                             │
│  Status: ActionError                        │
│  Handler: falco-terminal-shell              │
│                                             │
│  The original event will be re-published    │
│  to NATS. A new task will be created.       │
│                                             │
│              [ Cancel ]  [ Confirm ]        │
└─────────────────────────────────────────────┘
```

**Row navigation:** clicking anywhere on a row except the action buttons navigates to `/tasks/:id`.

### 6.2 Task Detail

**URL:** `/tasks/:id`

Full page navigation. Browser history is preserved — back button returns to the Tasks feed with URL params (filters) intact.

**Header:**

```
← Back to Tasks

Task 01JKXYZ8ABCDEFGH...   (full ULID, monospace)        [ Retry ]  or  [ Mark as Timeout ]

Handler: falco-terminal-shell    Namespace: kape-system    Status: ● ActionError
Received: 2026-04-03 14:22:01 UTC    Duration: 4.2s    Dry run: false
```

The action button renders based on status using identical logic to the feed row. Clicking opens the same confirmation modal. After the action completes, React Router revalidates the loader — the status badge and header update in-place.

**Four info panels (2×2 grid):**

**Panel 1 — Triggering Event**

Displays `event_type`, `event_source`, `event_id` as labelled fields. Below them, `event_raw` rendered as a syntax-highlighted, collapsible JSON block (collapsed by default to avoid overwhelming the view). The raw event is always available — it is stored immutably in PostgreSQL at Task creation and never requires an OTEL lookup.

**Panel 2 — LLM Decision**

Renders `schema_output` as a structured key-value list. Fields and labels are handler-specific (derived from the `KapeSchema` for this handler). Example:

```
Decision:          change-required
Confidence:        0.87
Reasoning:         Consolidation frequency has increased 4x in the
                   last 12h on nodepool general-purpose...
Estimated Impact:  high
Affected Nodepool: general-purpose
```

Renders `—` for all fields when `schema_output` is null — i.e. the task terminated before reaching `parse_output`.

**Panel 3 — Actions**

One row per `ActionResult` in `tasks.actions`:

```
● Completed   request-gitops-pr     event-emitter
● Failed      notify-slack          webhook          connection timeout after 5s
● Skipped     store-investigation   save-memory      (dry run)
```

Status dot colours: green (Completed), red (Failed), grey (Skipped). Error detail renders inline beneath the failed row — no expansion toggle needed.

Renders "No actions recorded" when `actions` is null.

**Panel 4 — Error**

Visible only when `error` is non-null. Displayed fields:

```
Type:    SchemaValidationFailed
Detail:  Field 'confidence' must be between 0 and 1; got 1.4
Schema:  karpenter-decision-schema
```

For `UnhandledError`, a `[ Show traceback ]` toggle reveals the Python traceback in a monospace `<pre>` block. Collapsed by default.

Panel is hidden entirely when `error` is null (i.e. `Completed` tasks).

**Footer:**

```
OTEL Trace: 4bf92f3577b34da6a8...    [ Open in SigNoz ↗ ]

Retry of: 01JKWWW...    [ View chain ]
```

`otel_trace_id` null → renders "Trace unavailable" with muted tooltip: "Pod may have crashed before tracer was initialised."

`retry_of` null → footer row is omitted entirely.

### 6.3 Retry Lineage Chain

**URL:** `/tasks/:id/lineage`

Reachable from the `[ View chain ]` link in the Task detail footer, or from the `[ View chain ]` row action on `Retried` tasks in the feed.

The `:id` may be any task in the chain. The loader walks `retry_of` upward to find the root (the task with no `retry_of`), then fetches all tasks sharing that root via `GET /tasks/:id/lineage` on `kape-task-service`.

**Layout:**

```
Lineage: 01JKWWW... → 01JKXYZ... → 01JKQQQ...   (3 executions)

┌──────────────────────────────────────────────────────────────────────┐
│  #1  01JKWWW...   ● ActionError    2026-04-03 14:10:01   4.1s  [ View detail ] │
├──────────────────────────────────────────────────────────────────────┤
│  #2  01JKXYZ...   ● ActionError    2026-04-03 14:22:01   4.2s  [ View detail ] │
├──────────────────────────────────────────────────────────────────────┤
│  #3  01JKQQQ...   ● Completed      2026-04-03 14:35:44   3.9s  [ View detail ] │
└──────────────────────────────────────────────────────────────────────┘
```

The task navigated from is highlighted. Each `[ View detail ]` link navigates to `/tasks/:id` for that specific execution.

### 6.4 Handlers

**URL:** `/handlers`

Derived entirely from task records via `kape-task-service` aggregate queries. No Kubernetes API call is made. Replica count, throughput p99, and LLM latency are Prometheus concerns surfaced in Grafana — not replicated here.

**Table columns:**

| Column         | Source                                                                 | Sortable           | Notes                                          |
| -------------- | ---------------------------------------------------------------------- | ------------------ | ---------------------------------------------- |
| Handler        | `tasks.handler` distinct                                               | Yes                | Links to `/handlers/:name`                     |
| Namespace      | `tasks.namespace`                                                      | Yes                |                                                |
| Last Task      | MAX `received_at` per handler                                          | Yes — default DESC | Relative time, absolute on hover               |
| Tasks (24h)    | COUNT `received_at > now()-24h`                                        | Yes                | Total volume indicator                         |
| Failures (24h) | COUNT `status IN (Failed, SchemaValidationFailed, ActionError)` in 24h | Yes                | Red count badge if > 0                         |
| Processing     | COUNT `status = Processing`                                            | Yes                | Amber count badge if > 0; live-updated via SSE |
| Actions        | —                                                                      | No                 | `[ View Tasks ]`                               |

Default sort: Last Task descending (most recently active handler first). All numeric columns sortable client-side.

`[ View Tasks ]` navigates to `/tasks?handler=<name>` — the Tasks feed pre-filtered to that handler.

Clicking a handler name navigates to `/handlers/:name`.

### 6.5 Handler Detail

**URL:** `/handlers/:name`

**Section 1 — Summary strip:**

```
falco-terminal-shell                                          [ View Tasks ]

Namespace: kape-system    Last task: 2 minutes ago    Tasks today: 147    Failures today: 3
```

`[ View Tasks ]` navigates to `/tasks?handler=falco-terminal-shell`.

**Section 2 — Decision distribution:**

Time range selector: `[ Last 1h ]  [ 6h ]  [ 24h ]  [ 7d ]`

A horizontal stacked bar chart showing the proportion of each `schema_output.decision` value among `Completed` tasks in the selected window. Below it, a breakdown table:

```
Decision              Count    % of Completed
──────────────────────────────────────────────
ignore                89       72%
investigate           28       23%
change-required        6        5%

Non-completed tasks in window: 24
  └─ Failed: 3  |  ActionError: 8  |  Processing: 13
```

Non-completed tasks are broken out separately. They do not have a `schema_output.decision` value and are excluded from the percentage calculation. Surfacing them explicitly prevents the operator from misreading a skewed denominator as a clean distribution.

If `schema_output.decision` field names vary per `KapeSchema`, the distribution renders whatever values are present in the data — it is not hardcoded to specific decision strings.

---

## 7. Real-Time Updates

### 7.1 Transport: Server-Sent Events (SSE)

The live feed and the Processing count on the Handlers table are updated via SSE. WebSockets are not used — the dashboard is server-push only. SSE is HTTP/1.1 compatible and natively supported by browsers via `EventSource`.

`kape-task-service` exposes a `GET /tasks/stream` endpoint that emits task events as they are written or updated. The React Router v7 SSE resource route at `GET /sse/tasks` proxies this stream, appending session validation before forwarding to the browser.

### 7.2 Event Types

The SSE stream emits two event types:

**`task.created`** — a new Task has been written (on NATS ACK). Payload: full Task object.

**`task.updated`** — a Task status has changed (handler completion, operator Timeout/Retry). Payload: full Task object.

The frontend handles both identically: upsert the task into the local feed state by ID. New tasks are prepended to the top; existing tasks update in-place with a brief highlight animation on the status badge.

### 7.3 Pause / Resume

The `● Live` indicator in the Tasks feed filter bar acts as a pause toggle. When paused:

- The SSE connection remains open
- Incoming events are buffered in a React ref (not state — no re-render)
- A banner appears: "Feed paused — 4 new tasks waiting. [ Resume ]"
- Clicking `Resume` flushes the buffer into state and re-enables live rendering

This prevents the table from jumping while the operator is reading a specific row.

### 7.4 Reconnection

`remix-utils` `useEventSource` handles reconnection automatically with exponential backoff. When the SSE connection drops, the `● Live` indicator turns red with tooltip "Reconnecting...". On successful reconnection the feed performs a full reload via React Router revalidation to catch any events missed during the disconnect window.

### 7.5 Elapsed Timers

`Processing` task elapsed times tick client-side. The feed loader provides `received_at` as an ISO timestamp. A single `setInterval` at 1-second resolution iterates all visible `Processing` rows and updates their elapsed display. No per-row timer — one shared interval for the entire visible set.

---

## 8. API Surface

The dashboard is the sole consumer of `kape-task-service` endpoints. All calls are made server-side from React Router loaders and actions — the browser never calls `kape-task-service` directly.

**Read endpoints (loaders):**

| Method | Path                                         | Used by                                |
| ------ | -------------------------------------------- | -------------------------------------- |
| `GET`  | `/tasks?handler=X&status=Y&since=Z&limit=50` | Tasks feed loader                      |
| `GET`  | `/tasks/:id`                                 | Task detail loader                     |
| `GET`  | `/tasks/:id/lineage`                         | Lineage chain loader                   |
| `GET`  | `/tasks/stream`                              | SSE resource route (proxied)           |
| `GET`  | `/handlers`                                  | Handlers table loader (aggregate)      |
| `GET`  | `/tasks/decisions?handler=X&since=Z`         | Handler detail — decision distribution |

**Write endpoints (actions):**

| Method  | Path                 | Used by               |
| ------- | -------------------- | --------------------- |
| `POST`  | `/tasks/:id/retry`   | Retry action          |
| `PATCH` | `/tasks/:id/status`  | Single Timeout action |
| `PATCH` | `/tasks/bulk/status` | Bulk Timeout action   |

---

## 9. Deployment

### 9.1 Kubernetes Resources

The Helm chart ships two Deployments in `kape-system`: `oauth2-proxy` and `kape-dashboard`.

**OAuth2 Proxy Deployment:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: oauth2-proxy
  namespace: kape-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: oauth2-proxy
  template:
    metadata:
      labels:
        app: oauth2-proxy
    spec:
      containers:
        - name: oauth2-proxy
          image: quay.io/oauth2-proxy/oauth2-proxy:v7
          args:
            - --provider=github
            - --github-org={{ .Values.auth.github.org }}
            - --github-team={{ .Values.auth.github.team }}
            - --upstream=http://kape-dashboard.kape-system:3000
            - --http-address=0.0.0.0:4180
            - --redirect-url=https://{{ .Values.ingress.host }}/oauth2/callback
            - --email-domain=*
            - --cookie-secure=true
            - --cookie-samesite=strict
          env:
            - name: OAUTH2_PROXY_CLIENT_ID
              valueFrom:
                secretKeyRef:
                  name: kape-dashboard-github-oauth
                  key: client_id
            - name: OAUTH2_PROXY_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: kape-dashboard-github-oauth
                  key: client_secret
            - name: OAUTH2_PROXY_COOKIE_SECRET
              valueFrom:
                secretKeyRef:
                  name: kape-dashboard-oauth2-proxy
                  key: cookie_secret
          ports:
            - containerPort: 4180
```

**kape-dashboard Deployment:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-dashboard
  namespace: kape-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kape-dashboard
  template:
    metadata:
      labels:
        app: kape-dashboard
    spec:
      containers:
        - name: kape-dashboard
          image: ghcr.io/kape-io/kape-dashboard:v1
          ports:
            - containerPort: 3000
          env:
            - name: TASK_SERVICE_URL
              value: "http://kape-task-service.kape-system:8080"
```

The dashboard has no auth-related environment variables. Identity is read from `X-Forwarded-User` and `X-Forwarded-Email` headers injected by OAuth2 Proxy on every forwarded request.

### 9.2 Secrets

Two secrets are required at install time:

```yaml
# GitHub OAuth App credentials
# Create at: github.com/organizations/<org>/settings/applications
apiVersion: v1
kind: Secret
metadata:
  name: kape-dashboard-github-oauth
  namespace: kape-system
stringData:
  client_id: "<github-oauth-app-client-id>"
  client_secret: "<github-oauth-app-client-secret>"

---
# OAuth2 Proxy cookie encryption key
# Generate with: openssl rand -base64 32
apiVersion: v1
kind: Secret
metadata:
  name: kape-dashboard-oauth2-proxy
  namespace: kape-system
stringData:
  cookie_secret: "<32-byte-base64-random-value>"
```

The Helm chart documents these as required pre-install secrets — it does not generate them automatically.

### 9.3 NetworkPolicy

Two NetworkPolicy resources are required:

**Restrict `kape-task-service` ingress to dashboard only:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-task-service-ingress
  namespace: kape-system
spec:
  podSelector:
    matchLabels:
      app: kape-task-service
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: kape-dashboard
      ports:
        - protocol: TCP
          port: 8080
```

**Restrict `kape-dashboard` ingress to OAuth2 Proxy only:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-dashboard-ingress
  namespace: kape-system
spec:
  podSelector:
    matchLabels:
      app: kape-dashboard
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: oauth2-proxy
      ports:
        - protocol: TCP
          port: 3000
```

This ensures the dashboard is only reachable via OAuth2 Proxy — direct access bypassing authentication is blocked at the network layer.

### 9.4 Ingress

The Ingress routes all traffic to OAuth2 Proxy. The dashboard Service is cluster-internal only.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kape-dashboard
  namespace: kape-system
spec:
  ingressClassName: "{{ .Values.ingress.className }}"
  rules:
    - host: "{{ .Values.ingress.host }}"
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: oauth2-proxy
                port:
                  number: 4180
  tls:
    - hosts:
        - "{{ .Values.ingress.host }}"
      secretName: "{{ .Values.ingress.tls.secretName }}"
```

Engineers not using an Ingress can port-forward directly to OAuth2 Proxy:

```bash
kubectl port-forward -n kape-system svc/oauth2-proxy 4180:4180
```

---

## 10. Observability Boundary

This table makes explicit what the dashboard owns vs what it delegates.

| Concern                               | Owner                              | How dashboard surfaces it         |
| ------------------------------------- | ---------------------------------- | --------------------------------- |
| Task lifecycle, status, timing        | PostgreSQL via `kape-task-service` | REST API calls in loaders         |
| LLM decision output (`schema_output`) | PostgreSQL via `kape-task-service` | Rendered in Task Detail Panel 2   |
| Action outcomes                       | PostgreSQL via `kape-task-service` | Rendered in Task Detail Panel 3   |
| Triggering event (raw CloudEvent)     | PostgreSQL via `kape-task-service` | Rendered in Task Detail Panel 1   |
| Every MCP tool call during ReAct loop | OTEL backend (SigNoz / Tempo)      | External link via `otel_trace_id` |
| LLM prompt and response text          | OTEL backend                       | External link via `otel_trace_id` |
| LLM token counts, iteration count     | OTEL backend                       | External link via `otel_trace_id` |
| Handler throughput, latency p99       | Prometheus                         | Grafana (not surfaced here)       |
| Handler failure rate                  | Prometheus                         | Grafana (not surfaced here)       |

---

## 11. Future Work

### RBAC — Viewer and Operator Roles (v2)

v1 grants full access to any authenticated GitHub team member. v2 will introduce two roles:

| Role       | Capabilities                                                           |
| ---------- | ---------------------------------------------------------------------- |
| `viewer`   | All read operations — Tasks feed, Task detail, Handlers, lineage chain |
| `operator` | All viewer capabilities + Timeout (single and bulk) + Retry            |

Implementation approach with OAuth2 Proxy + GitHub:

- Map two GitHub teams to the two roles (e.g. `platform-viewers` → `viewer`, `platform-operators` → `operator`)
- OAuth2 Proxy injects the user's GitHub teams in a configurable header (e.g. `X-Forwarded-Groups`)
- The React Router v7 root loader reads this header and sets the role in request context
- Write-action route handlers (`POST /tasks/:id/retry`, `PATCH /tasks/*/status`) check the role and return `403` for `viewer`
- Write-action UI elements (Timeout, Retry buttons) are absent from the DOM for `viewer` users

This is deferred because it requires two GitHub teams to be maintained in sync with dashboard access, and the OPA enforcement layer adds operational complexity. v1 relies on GitHub team membership as the single access gate — anyone in the team is an operator.

### Approval Management (v2)

`PendingApproval` task status is schema-ready in v1 but has no associated UI action. v2 will add an approval workflow view — surfacing pending approval tasks and allowing operators to approve or reject them, triggering the Argo Workflows suspend node resolution.

### Handler-Scoped Task Feed

v1 navigates from the Handlers table to the Tasks feed with a pre-applied handler filter. A future iteration may introduce a dedicated handler-scoped feed page with handler-specific context rendered inline alongside the task list.
