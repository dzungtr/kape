# KAPE Network Policy Reference Manifests

Reference NetworkPolicy manifests for the 5-boundary network isolation model.

**Design spec:** `docs/superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md`
**Reference spec:** 0007 (security layer)
**GitHub issue:** dzungtr/kape#77

---

## The 5-Boundary Model

| # | Name | Policy type | Direction | Load-bearing rule |
|---|------|-------------|-----------|-------------------|
| 1 | Handler pod egress | Egress on handler pods | Outbound | Private CIDR exclusion on 443 forces MCP through sidecar |
| 2 | kapetool sidecar egress | Egress on handler pods (per-tool label) | Outbound | Per-KapeTool policy restricts pod to one MCP server |
| 3 | MCP server ingress | Ingress on MCP server pods | Inbound | Only handler pods in kape-system may connect |
| 4 | kape-task-service ingress | Ingress on task-service pods | Inbound | Only handler and dashboard pods may connect |
| 5 | postgres ingress | Ingress on postgres pods | Inbound | Only task-service pods may connect |

The full architectural rationale and boundary diagrams are in the design spec and spec 0007 section 2.

---

## Directory Layout

```
examples/networkpolicy/
  standard/          Standard Kubernetes NetworkPolicy API (CNI-agnostic)
    handler-egress.yaml
    kapetool-egress.yaml
    mcp-server-ingress.yaml
    task-service-ingress.yaml
    postgres-ingress.yaml
  cilium/            CiliumNetworkPolicy (requires Cilium CNI)
    handler-egress.yaml
    kapetool-egress.yaml
    mcp-server-ingress.yaml
    task-service-ingress.yaml
    postgres-ingress.yaml
  README.md          (this file)
```

---

## Standard vs Cilium Variant

Use `standard/` when your cluster runs any NetworkPolicy-capable CNI (Calico, Flannel
with network policy add-on, etc.). These manifests use only the `networking.k8s.io/v1`
API and are portable across CNIs.

Use `cilium/` when your cluster runs Cilium. The Cilium variant is strictly stronger
(per D2 in the design spec):

- Handler egress to LLM providers is restricted by FQDN (`api.anthropic.com`,
  `api.openai.com`) rather than port only. A compromised handler pod cannot reach
  arbitrary internet hosts on port 443 — only the named endpoints are reachable.
- Cluster-internal deny is expressed as an explicit `egressDeny` to the `cluster`
  entity rather than private CIDR exclusions, which is more explicit and harder to
  misconfigure.

Do not mix standard and Cilium manifests in the same cluster.

---

## Required Pod Labels

Engineers must set the following labels on their pods before applying these policies.

| Label | Value | Who sets it | Applied to |
|-------|-------|-------------|------------|
| `kape.io/component` | `nats` | Engineer | NATS pods |
| `kape.io/component` | `task-service` | Engineer | kape-task-service pods |
| `kape.io/component` | `dashboard` | Engineer | Dashboard pods |
| `kape.io/component` | `postgres` | Engineer | Postgres pods |
| `kape.io/mcp-server` | `<server-name>` | Engineer | MCP server pods |

The operator sets the following labels automatically at Deployment creation time:

| Label | Value | Who sets it | Applied to |
|-------|-------|-------------|------------|
| `kape.io/component` | `handler` | Operator | Handler pods |
| `kape.io/tool` | `<tool-name>` | Operator | Handler pods (per KapeTool) |

---

## kapetool-egress.yaml is a Template

`kapetool-egress.yaml` is an example for the KapeTool named `k8s-mcp-read`. It is
**not** a singleton — you must create one NetworkPolicy per KapeTool instance (per D4
in the design spec).

To add a KapeTool:

1. Copy `kapetool-egress.yaml` (or `cilium/kapetool-egress.yaml`).
2. Change `metadata.name` to `kape-kapetool-egress-<tool-name>`.
3. Change `kape.io/tool: k8s-mcp-read` to `kape.io/tool: <tool-name>`.
4. Change `kape.io/mcp-server: k8s-mcp` to `kape.io/mcp-server: <server-name>`.
5. Apply the new manifest.

---

## No Default-Deny Shipped

These manifests are additive allowances. A cluster-wide default-deny NetworkPolicy is
**not** included (per D7 in the design spec). Engineers must apply a deny-all baseline
independently as part of their cluster setup. The policies here are meaningless without
a pre-existing deny posture.

---

## Applying the Manifests

### Standard variant

```bash
# Dry-run (server-side) — verify the API server accepts all manifests
kubectl apply --dry-run=server -f examples/networkpolicy/standard/

# Apply
kubectl apply -f examples/networkpolicy/standard/
```

### Cilium variant

```bash
# Dry-run (server-side) — requires Cilium CRDs to be installed
kubectl apply --dry-run=server -f examples/networkpolicy/cilium/

# Apply
kubectl apply -f examples/networkpolicy/cilium/
```

---

## Live-Cluster Verification

The acceptance criteria and step-by-step verification commands (boundaries 1-5) are
documented in the design spec:

`docs/superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md`

See the "Acceptance Criteria" and "Testing Strategy" sections for boundary-by-boundary
`kubectl exec` checks and expected pass/fail outcomes.
