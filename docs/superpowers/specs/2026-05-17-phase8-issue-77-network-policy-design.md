# Phase 8.2 — Network Policy Manifests

**Status:** Draft
**Date:** 2026-05-17
**GitHub Issue:** #77
**Phase:** 08-audit-security
**Milestone:** M4
**Reference Specs:** 0007

---

## Goal

Ship reference NetworkPolicy manifests that enforce the full 5-boundary network
isolation model defined in spec 0007 section 2. Manifests are delivered in two CNI
dialects — standard Kubernetes NetworkPolicy API and Cilium NetworkPolicy. Engineers
apply these manifests manually during cluster setup; the operator does not generate
them automatically.

---

## Background

Without network isolation, any compromised pod in the cluster can reach MCP servers,
NATS, and the audit database directly — bypassing the kapetool sidecar, bypassing the
`allowedTools` allowlist, and bypassing everything else in the security stack.

The iteration file (phase 8.2) described only the handler-egress boundary. This spec
implements the full 5-boundary model from spec 0007 section 2, which is required to
close the perimeter correctly. Delivering only boundary 1 leaves MCP servers, postgres,
and task-service reachable from arbitrary pods in the cluster.

The key load-bearing clause in the handler-egress policy is the `ipBlock` exception
list for private CIDRs on port 443. Because all cluster-internal addresses fall within
`10.0.0.0/8`, `172.16.0.0/12`, or `192.168.0.0/16`, a handler pod cannot reach any
in-cluster service on port 443 — including MCP servers. All MCP tool calls must go
through the kapetool sidecar on localhost (`127.0.0.1`), which is not a network hop
subject to NetworkPolicy.

---

## Architecture

### The 5-boundary model

```
                         ┌──────────────────────────────────────────┐
                         │              kape-system namespace         │
                         │                                            │
   ┌────────────┐        │  ┌────────────────────────────────────┐   │
   │ LLM API    │◄─────────│  handler pod                        │   │
   │ (internet) │  B1 443  │  ┌──────────────────────────────┐  │   │
   │ Anthropic  │  excl.   │  │ kapetool sidecar (localhost)  │  │   │
   │ OpenAI     │  priv.   │  └─────────────────┬────────────┘  │   │
   └────────────┘  CIDRs   │        │            │               │   │
                         │  └───────┼────────────┼───────────────┘   │
                         │          │            │                    │
                         │          │ B1 4222    │ B2 8080/8081       │
                         │          ▼            ▼                    │
                         │  ┌──────────┐   ┌───────────┐             │
                         │  │  NATS    │   │ MCP server│◄── B3       │
                         │  │ :4222    │   │ :8080/8081│  (ingress   │
                         │  └──────────┘   └───────────┘   from      │
                         │                                  handler   │
                         │  ┌──────────────────────┐        only)    │
                         │  │  kape-task-service    │◄── B4           │
                         │  │       :8080           │  (handler +     │
                         │  └──────────┬────────────┘   dashboard)   │
                         │             │ B4 5432 (task-service only) │
                         │             ▼                              │
                         │  ┌──────────────────────┐                 │
                         │  │      postgres         │◄── B5           │
                         │  │       :5432           │  (task-svc     │
                         │  └──────────────────────┘   only)         │
                         └──────────────────────────────────────────┘
```

### Boundary summary

| # | Name | Policy type | Direction | Load-bearing rule |
|---|------|-------------|-----------|-------------------|
| 1 | Handler pod egress | Egress on handler pods | Outbound | Private CIDR exclusion on 443 forces MCP through sidecar |
| 2 | kapetool sidecar egress | Egress on handler pods (per-tool label) | Outbound | Per-KapeTool NetworkPolicy restricts pod to one MCP server |
| 3 | MCP server ingress | Ingress on MCP server pods | Inbound | Only handler pods in kape-system may connect |
| 4 | kape-task-service ingress | Ingress on task-service pods | Inbound | Only handler and dashboard pods may connect |
| 5 | postgres ingress | Ingress on postgres pods | Inbound | Only task-service pods may connect |

### Pod labels

| Label | Value | Set by | Applied to |
|-------|-------|--------|-----------|
| `kape.io/component` | `handler` | Operator | Handler pods |
| `kape.io/component` | `nats` | Engineer | NATS pods |
| `kape.io/component` | `task-service` | Engineer | task-service pods |
| `kape.io/component` | `dashboard` | Engineer | Dashboard pods |
| `kape.io/component` | `postgres` | Engineer | Postgres pods |
| `kape.io/tool` | `<tool-name>` | Operator | Handler pods (per KapeTool) |
| `kape.io/mcp-server` | `<server-name>` | Engineer | MCP server pods |

### Directory structure

```
examples/networkpolicy/
  standard/
    handler-egress.yaml
    kapetool-egress.yaml
    mcp-server-ingress.yaml
    task-service-ingress.yaml
    postgres-ingress.yaml
  cilium/
    handler-egress.yaml
    kapetool-egress.yaml
    mcp-server-ingress.yaml
    task-service-ingress.yaml
    postgres-ingress.yaml
  README.md
```

---

## Manifest Specifications

### Boundary 1 — Handler Pod Egress

Handler pods may reach: NATS on port 4222, kape-task-service on port 8080, and LLM
provider APIs on port 443 (internet only). All other egress is denied.

The `ipBlock` rule for LLM egress excludes private CIDRs. This is the load-bearing
clause: since all cluster-internal services use private IP space, the exclusions
prevent handler pods from bypassing the kapetool sidecar by reaching MCP servers
directly on port 443.

**`standard/handler-egress.yaml`:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-handler-egress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 1 — Handler pod egress.
      Permits NATS (4222), task-service (8080), and internet-only LLM API (443).
      Private CIDR exclusions on the LLM rule are load-bearing: they force all
      MCP traffic through the kapetool sidecar on localhost, not a direct
      cluster-internal connection.
spec:
  podSelector:
    matchLabels:
      kape.io/component: handler
  policyTypes:
    - Egress
  egress:
    # NATS JetStream — event subscription and publication
    - to:
        - podSelector:
            matchLabels:
              kape.io/component: nats
      ports:
        - port: 4222
          protocol: TCP

    # kape-task-service — task status writes and audit log
    - to:
        - podSelector:
            matchLabels:
              kape.io/component: task-service
      ports:
        - port: 8080
          protocol: TCP

    # LLM provider (Anthropic / OpenAI) — internet egress on 443 only.
    # Private IP ranges are excluded. This is the load-bearing security clause:
    # all cluster-internal addresses fall within these CIDRs, so MCP servers
    # cannot be reached directly from handler pods on port 443. MCP traffic
    # must go through the kapetool sidecar on localhost (127.0.0.1), which is
    # not a network hop subject to NetworkPolicy.
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              - 10.0.0.0/8
              - 172.16.0.0/12
              - 192.168.0.0/16
      ports:
        - port: 443
          protocol: TCP

    # DNS — required for hostname resolution (NATS service name, task-service, etc.)
    - to:
        - namespaceSelector: {}
      ports:
        - port: 53
          protocol: UDP
```

**`cilium/handler-egress.yaml`:**

The Cilium variant is strictly stronger: it permits LLM egress by FQDN rather than by
port. A compromised handler pod cannot reach arbitrary internet hosts on port 443 —
only the named LLM provider endpoints are reachable. The cluster-internal deny rule
replaces the private CIDR exclusion approach with an explicit `egressDeny` to all
cluster entities.

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: kape-handler-egress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 1 — Handler pod egress (Cilium variant).
      Stronger than standard: LLM egress is FQDN-restricted, not port-only.
      The egressDeny to 'cluster' blocks all cluster-internal egress except
      the explicit NATS and task-service rules below.
spec:
  endpointSelector:
    matchLabels:
      kape.io/component: handler
  # Block all cluster-internal egress by default.
  # MCP traffic must go through the kapetool sidecar on localhost (exempt from
  # NetworkPolicy as a loopback interface).
  egressDeny:
    - toEntities:
        - cluster
  egress:
    # NATS JetStream — whitelisted cluster-internal destination
    - toEndpoints:
        - matchLabels:
            kape.io/component: nats
      toPorts:
        - ports:
            - port: "4222"
              protocol: TCP

    # kape-task-service — whitelisted cluster-internal destination
    - toEndpoints:
        - matchLabels:
            kape.io/component: task-service
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP

    # LLM provider — FQDN-restricted internet egress.
    # Only Anthropic and OpenAI endpoints are permitted.
    # Add additional FQDNs here if your deployment uses other LLM providers.
    - toFQDNs:
        - matchName: "api.anthropic.com"
        - matchName: "api.openai.com"
      toPorts:
        - ports:
            - port: "443"
              protocol: TCP

    # DNS — required for FQDN resolution
    - toEntities:
        - kube-dns
      toPorts:
        - ports:
            - port: "53"
              protocol: UDP
```

---

### Boundary 2 — kapetool Sidecar Egress (per KapeTool)

Because NetworkPolicy applies at the pod level (not the container level), sidecar
egress is controlled by labelling handler pods with the tool they host and writing
one NetworkPolicy per KapeTool instance. The operator sets `kape.io/tool: <tool-name>`
on handler pods at Deployment creation time.

The manifests below show the example for `k8s-mcp-read`. Engineers create one
NetworkPolicy per KapeTool, substituting the tool name and MCP server label.

**`standard/kapetool-egress.yaml` (example: `k8s-mcp-read`):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-kapetool-egress-k8s-mcp-read
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 2 — kapetool sidecar egress for k8s-mcp-read.
      One NetworkPolicy per KapeTool instance. The operator sets
      kape.io/tool: <tool-name> on handler pods. Engineers label
      their MCP server pods with kape.io/mcp-server: <server-name>.
      Duplicate this manifest for each additional KapeTool, changing
      the name, kape.io/tool selector, and kape.io/mcp-server selector.
spec:
  podSelector:
    matchLabels:
      kape.io/component: handler
      kape.io/tool: k8s-mcp-read   # operator sets this on handler pods at Deployment creation
  policyTypes:
    - Egress
  egress:
    # MCP server — both SSE (:8080) and Streamable HTTP (:8081) transports.
    # Engineers label MCP server pods with kape.io/mcp-server: <server-name>.
    - to:
        - podSelector:
            matchLabels:
              kape.io/mcp-server: k8s-mcp   # engineer sets this on MCP server pods
      ports:
        - port: 8080
          protocol: TCP
        - port: 8081
          protocol: TCP
```

**`cilium/kapetool-egress.yaml` (example: `k8s-mcp-read`):**

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: kape-kapetool-egress-k8s-mcp-read
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 2 — kapetool sidecar egress for k8s-mcp-read (Cilium variant).
      Functionally equivalent to the standard variant. Uses Cilium endpoint
      selectors for consistent policy model with the handler-egress policy.
spec:
  endpointSelector:
    matchLabels:
      kape.io/component: handler
      kape.io/tool: k8s-mcp-read
  egress:
    - toEndpoints:
        - matchLabels:
            kape.io/mcp-server: k8s-mcp
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP
            - port: "8081"
              protocol: TCP
```

---

### Boundary 3 — MCP Server Ingress

MCP servers accept connections only from handler pods in the `kape-system` namespace.
This is a reference manifest for engineers to adapt to their MCP server Deployments.
The `namespaceSelector` + `podSelector` combination (AND semantics) ensures that only
`kape.io/component: handler` pods in `kape-system` can connect — not handler-labelled
pods in other namespaces.

**`standard/mcp-server-ingress.yaml`:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-mcp-ingress
  namespace: kape-system   # adjust to the namespace where your MCP server is deployed
  annotations:
    kape.io/doc: >
      Boundary 3 — MCP server ingress.
      Permits inbound connections only from kape.io/component: handler pods
      in the kape-system namespace. The namespaceSelector + podSelector
      combination uses AND semantics — both conditions must match.
      Engineer must set kape.io/mcp-server: <server-name> on MCP server pods.
      Adjust the podSelector.matchLabels to match your MCP server's label.
spec:
  podSelector:
    matchLabels:
      kape.io/mcp-server: k8s-mcp   # engineer sets this on their MCP server pods
  policyTypes:
    - Ingress
  ingress:
    # Accept connections only from handler pods in kape-system.
    # namespaceSelector + podSelector in the same 'from' entry = AND semantics.
    - from:
        - podSelector:
            matchLabels:
              kape.io/component: handler
          namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kape-system
      ports:
        - port: 8080
          protocol: TCP   # SSE transport
        - port: 8081
          protocol: TCP   # Streamable HTTP transport
```

**`cilium/mcp-server-ingress.yaml`:**

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: kape-mcp-ingress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 3 — MCP server ingress (Cilium variant).
      Permits inbound connections only from kape.io/component: handler pods
      in kape-system. Cilium uses endpointSelector for the target pod and
      fromEndpoints for the source constraint.
spec:
  endpointSelector:
    matchLabels:
      kape.io/mcp-server: k8s-mcp
  ingress:
    - fromEndpoints:
        - matchLabels:
            kape.io/component: handler
            k8s:io.kubernetes.pod.namespace: kape-system
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP
            - port: "8081"
              protocol: TCP
```

---

### Boundary 4 — kape-task-service Ingress

kape-task-service accepts connections from handler pods (task writes, schema output
persistence) and from dashboard pods (task reads). No other source is permitted.

**`standard/task-service-ingress.yaml`:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-task-service-ingress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 4 — kape-task-service ingress.
      Accepts connections from handler pods (task writes) and dashboard pods
      (task reads). All other ingress — including direct database clients and
      operator pods — is denied. This policy is load-bearing for audit log
      isolation: the REST API surface is the complete audit boundary.
spec:
  podSelector:
    matchLabels:
      kape.io/component: task-service
  policyTypes:
    - Ingress
  ingress:
    # Handler pods — task status writes and schema output persistence
    - from:
        - podSelector:
            matchLabels:
              kape.io/component: handler
      ports:
        - port: 8080
          protocol: TCP

    # Dashboard pods — task reads and status queries
    - from:
        - podSelector:
            matchLabels:
              kape.io/component: dashboard
      ports:
        - port: 8080
          protocol: TCP
```

**`cilium/task-service-ingress.yaml`:**

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: kape-task-service-ingress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 4 — kape-task-service ingress (Cilium variant).
spec:
  endpointSelector:
    matchLabels:
      kape.io/component: task-service
  ingress:
    # Handler pods
    - fromEndpoints:
        - matchLabels:
            kape.io/component: handler
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP

    # Dashboard pods
    - fromEndpoints:
        - matchLabels:
            kape.io/component: dashboard
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP
```

---

### Boundary 5 — postgres Ingress

Postgres accepts connections only from kape-task-service. No other component holds
database credentials or has a permitted network path. This policy enforces the v1
architectural isolation guarantee: the REST API surface of kape-task-service is the
complete audit boundary.

**`standard/postgres-ingress.yaml`:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kape-postgres-ingress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 5 — postgres ingress.
      Accepts connections only from kape-task-service. This enforces the v1
      single-accessor model: no handler pod, dashboard pod, or operator pod
      has a permitted network path to postgres. The REST API of task-service
      is the complete audit boundary (spec 0007 section 9).
spec:
  podSelector:
    matchLabels:
      kape.io/component: postgres
  policyTypes:
    - Ingress
  ingress:
    # Only kape-task-service may connect to postgres
    - from:
        - podSelector:
            matchLabels:
              kape.io/component: task-service
      ports:
        - port: 5432
          protocol: TCP
```

**`cilium/postgres-ingress.yaml`:**

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: kape-postgres-ingress
  namespace: kape-system
  annotations:
    kape.io/doc: >
      Boundary 5 — postgres ingress (Cilium variant).
spec:
  endpointSelector:
    matchLabels:
      kape.io/component: postgres
  ingress:
    - fromEndpoints:
        - matchLabels:
            kape.io/component: task-service
      toPorts:
        - ports:
            - port: "5432"
              protocol: TCP
```

---

## Design Decisions

### D1 — Full 5-boundary model, not handler-egress only

The iteration file described only boundary 1 (handler-egress). This spec implements
all 5 boundaries from spec 0007 section 2. Delivering only boundary 1 leaves MCP
servers, postgres, and task-service accessible from arbitrary pods in the cluster,
which does not close the perimeter.

### D2 — Two CNI variants: standard + Cilium

Both dialects are delivered. The standard Kubernetes NetworkPolicy API is CNI-agnostic
and usable in any cluster. The Cilium variant is strictly stronger — LLM egress is
FQDN-restricted rather than port-only, preventing a compromised handler pod from
reaching arbitrary internet hosts on port 443. KAPE does not impose a hard Cilium
dependency; engineers choose the variant matching their CNI.

### D3 — Private CIDR exclusion is the load-bearing handler-egress clause

The `ipBlock` except list (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`) on the
LLM egress rule is not cosmetic. It is the enforcement mechanism that forces all MCP
traffic through the kapetool sidecar on localhost. If these exclusions were absent,
handler pods could bypass the sidecar by reaching MCP servers directly on port 443
using their cluster-internal IP. The Cilium variant replaces this with an `egressDeny`
to the `cluster` entity plus explicit FQDN allowances, which is functionally equivalent
and more explicit.

### D4 — kapetool-egress is per-KapeTool

One NetworkPolicy per KapeTool instance. This design is forced by the fact that
NetworkPolicy applies at the pod level (not container level) and Kubernetes has no
concept of per-container network rules. The operator labels each handler pod with
`kape.io/tool: <tool-name>` at Deployment creation time, which enables per-tool
policy selection.

### D5 — Operator does not auto-generate NetworkPolicies

These are reference manifests. Engineers apply them manually as part of cluster setup.
Auto-generation is deferred to v2 (spec 0007 Known Gaps). This keeps the operator
scope tight and lets engineers control network policy as part of their GitOps
deployment manifests.

### D6 — Boundary 3 uses AND-semantics namespaceSelector + podSelector

In the standard Kubernetes NetworkPolicy API, placing `namespaceSelector` and
`podSelector` in the same `from` entry produces AND semantics (both must match). This
is intentional: it prevents handler-labelled pods in other namespaces from connecting
to MCP servers. Engineers who deploy MCP servers in a namespace other than `kape-system`
must adjust the `namespace` field and the `namespaceSelector` in this manifest.

### D7 — No default-deny policy shipped

A cluster-wide default-deny NetworkPolicy is not shipped as part of this deliverable.
Engineers must apply one independently as a cluster baseline. The reference manifests
in this spec are additive allowances on top of a pre-existing deny-all posture.

---

## Key Files

New files to be created (all new, none currently exist):

- `examples/networkpolicy/standard/handler-egress.yaml`
- `examples/networkpolicy/standard/kapetool-egress.yaml`
- `examples/networkpolicy/standard/mcp-server-ingress.yaml`
- `examples/networkpolicy/standard/task-service-ingress.yaml`
- `examples/networkpolicy/standard/postgres-ingress.yaml`
- `examples/networkpolicy/cilium/handler-egress.yaml`
- `examples/networkpolicy/cilium/kapetool-egress.yaml`
- `examples/networkpolicy/cilium/mcp-server-ingress.yaml`
- `examples/networkpolicy/cilium/task-service-ingress.yaml`
- `examples/networkpolicy/cilium/postgres-ingress.yaml`
- `examples/networkpolicy/README.md`

Reference specs consulted:

- `/home/tony/projects/kape-io/docs/specs/0007-security-layer/README.md` — authoritative source for all 5 boundaries
- `/home/tony/projects/kape-io/docs/roadmap/phases/08-audit-security/02-network-policy.md` — iteration scope

---

## Acceptance Criteria

### Functional

- [ ] Apply all standard manifests to a test cluster running a CNI that supports
      standard Kubernetes NetworkPolicy (e.g. Calico, Flannel with network policy
      add-on).
- [ ] Apply all Cilium manifests to a test cluster running Cilium.
- [ ] Each manifest passes `kubectl apply --dry-run=server` without error.
- [ ] Each manifest passes `kubectl apply --dry-run=client` without error.

### Boundary 1 — handler egress

- [ ] `curl 8.8.8.8` from handler pod fails (default deny egress).
- [ ] `curl -k https://8.8.8.8` from handler pod fails (internet IP on 443 fails —
      direct IP, not FQDN, blocked by CIDR exception).
- [ ] `curl nats-svc:4222` from handler pod succeeds.
- [ ] `curl task-service-svc:8080` from handler pod succeeds.
- [ ] `curl https://api.anthropic.com` from handler pod succeeds (standard variant:
      FQDN resolves to public IP, not excluded by private CIDR list).
- [ ] `curl https://api.anthropic.com` from handler pod succeeds (Cilium variant:
      FQDN is explicitly permitted).
- [ ] `curl https://some-other-site.com` from handler pod fails (Cilium variant only:
      non-whitelisted FQDN on 443 is denied by the cluster-entity deny rule).
- [ ] MCP sidecar call from handler runtime succeeds (localhost — not subject to
      NetworkPolicy).

### Boundary 2 — kapetool egress

- [ ] Handler pod with `kape.io/tool: k8s-mcp-read` can reach MCP server pod with
      `kape.io/mcp-server: k8s-mcp` on ports 8080 and 8081.
- [ ] Handler pod without `kape.io/tool: k8s-mcp-read` label cannot reach the same
      MCP server pod on ports 8080 or 8081.

### Boundary 3 — MCP server ingress

- [ ] `curl mcp-server-svc:8080` from a handler pod in `kape-system` succeeds.
- [ ] `curl mcp-server-svc:8080` from a non-handler pod in `kape-system` fails.
- [ ] `curl mcp-server-svc:8080` from a handler-labelled pod in a different namespace
      fails.

### Boundary 4 — task-service ingress

- [ ] `curl task-service-svc:8080` from a handler pod succeeds.
- [ ] `curl task-service-svc:8080` from a dashboard pod succeeds.
- [ ] `curl task-service-svc:8080` from any other pod (e.g. a debug pod) fails.

### Boundary 5 — postgres ingress

- [ ] `psql -h postgres-svc -p 5432` from a task-service pod succeeds (connection
      reaches the port; auth is a separate concern).
- [ ] `psql -h postgres-svc -p 5432` from a handler pod fails.
- [ ] `psql -h postgres-svc -p 5432` from a dashboard pod fails.
- [ ] `psql -h postgres-svc -p 5432` from any other pod fails.

---

## Testing Strategy

### Prerequisites

1. A test cluster with a NetworkPolicy-capable CNI (Calico recommended for standard
   variant; Cilium for the Cilium variant).
2. Namespace `kape-system` exists with the label
   `kubernetes.io/metadata.name: kape-system` (set automatically by Kubernetes 1.21+).
3. Pods deployed with correct `kape.io/component` labels as documented in the
   Architecture section.

### Step 1 — Apply policies and verify they parse

```bash
kubectl apply --dry-run=server -f examples/networkpolicy/standard/
kubectl apply -f examples/networkpolicy/standard/

# For Cilium cluster:
kubectl apply --dry-run=server -f examples/networkpolicy/cilium/
kubectl apply -f examples/networkpolicy/cilium/
```

### Step 2 — Verify Boundary 1 (handler egress)

```bash
# Should fail — arbitrary internet IP blocked
kubectl exec -n kape-system deploy/handler -- curl -m 5 http://8.8.8.8 && echo FAIL || echo PASS

# Should fail — non-permitted internet HTTPS (Cilium variant only)
kubectl exec -n kape-system deploy/handler -- curl -m 5 -k https://example.com && echo FAIL || echo PASS

# Should succeed — NATS
kubectl exec -n kape-system deploy/handler -- curl -m 5 nats-svc:4222 && echo PASS || echo FAIL

# Should succeed — task-service
kubectl exec -n kape-system deploy/handler -- curl -m 5 task-service-svc:8080/health && echo PASS || echo FAIL
```

### Step 3 — Verify Boundary 2 (kapetool egress)

```bash
# Deploy a handler pod with the tool label
kubectl run -n kape-system test-handler \
  --image=curlimages/curl:latest \
  --labels='kape.io/component=handler,kape.io/tool=k8s-mcp-read' \
  -- sleep 3600

# Deploy a handler pod without the tool label
kubectl run -n kape-system test-handler-no-tool \
  --image=curlimages/curl:latest \
  --labels='kape.io/component=handler' \
  -- sleep 3600

# With tool label — should reach MCP server (port 8080)
kubectl exec -n kape-system test-handler -- curl -m 5 mcp-server-svc:8080 && echo PASS || echo FAIL

# Without tool label — should fail (no kapetool-egress NetworkPolicy matches)
kubectl exec -n kape-system test-handler-no-tool -- curl -m 5 mcp-server-svc:8080 && echo FAIL || echo PASS
```

### Step 4 — Verify Boundary 3 (MCP server ingress)

```bash
# Deploy a non-handler pod in kape-system
kubectl run -n kape-system test-non-handler \
  --image=curlimages/curl:latest \
  -- sleep 3600

# Handler pod — should succeed
kubectl exec -n kape-system test-handler -- curl -m 5 mcp-server-svc:8080 && echo PASS || echo FAIL

# Non-handler pod in kape-system — should fail
kubectl exec -n kape-system test-non-handler -- curl -m 5 mcp-server-svc:8080 && echo FAIL || echo PASS

# Handler-labelled pod in a different namespace — should fail
kubectl run -n default test-cross-ns-handler \
  --image=curlimages/curl:latest \
  --labels='kape.io/component=handler' \
  -- sleep 3600
kubectl exec -n default test-cross-ns-handler -- curl -m 5 <mcp-server-cluster-ip>:8080 && echo FAIL || echo PASS
```

### Step 5 — Verify Boundary 4 (task-service ingress)

```bash
# Handler — should succeed
kubectl exec -n kape-system test-handler -- curl -m 5 task-service-svc:8080/health && echo PASS || echo FAIL

# Non-handler pod — should fail
kubectl exec -n kape-system test-non-handler -- curl -m 5 task-service-svc:8080/health && echo FAIL || echo PASS
```

### Step 6 — Verify Boundary 5 (postgres ingress)

```bash
# task-service pod — should reach port 5432
kubectl exec -n kape-system deploy/task-service -- \
  bash -c 'timeout 5 bash -c "echo >/dev/tcp/postgres-svc/5432" && echo PASS || echo FAIL'

# Handler pod — should fail
kubectl exec -n kape-system test-handler -- \
  bash -c 'timeout 5 bash -c "echo >/dev/tcp/postgres-svc/5432" && echo FAIL || echo PASS'
```

### Cleanup

```bash
kubectl delete pod -n kape-system test-handler test-handler-no-tool test-non-handler
kubectl delete pod -n default test-cross-ns-handler
```
