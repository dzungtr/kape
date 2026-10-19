# KapeAgentServer CR: declarative serving environment, operator re-scoped

## Status

accepted

## Context

The greenfield direction (#161) deprecated the old operator — "the controller already owns the CRUD job". Meanwhile #260 established that sandbox agents have no runtime credential channel, and the profile chain (#157, corrected by ADR-0081) left its reconciler fork open: "thin CRD or controller job". The design session for #260 (spec #261) resolved both threads together: the profile/credential bundle needs a declarative home, and the controller must remain a pure tool surface (env in, 8-tool contract out — no CRDs, no k8s client, no profile registry).

## Decision

A new namespace-scoped CR, **`KapeAgentServer`**, declares one project serving environment: controller image, sandbox defaults, model gateway, a generic credential registry, network resources, and a free-text `description` (served as MCP server `instructions`). The **kape operator** owns its reconciliation — rendering the controller Deployment (env is the operator→controller contract), network resources, and OpenShell-side records. This **corrects #161's operator-deprecated posture**: the operator lives on, re-scoped to profile/credential reconciliation; it does not resume the old KapeHandler materialisation role.

Considered and rejected: a controller-job reconciler (couples the controller to k8s, splits the "gateway = control plane" preference's writer side); extending the deprecated KapeHandler CRD (#161 forbids extension); naming the CR `KapeController` (collides with the kape-controller binary/Deployment it materialises — CRs name intent, not artifacts).

## Consequences

- `kubectl get kapeagentservers` lists serving environments; each materializes one kape-controller Deployment in its namespace.
- Per-call credential fields are permanently absent from `create_agent` — callers name nothing; the profile provisions everything. This is the deliberate anti-bias/anti-miss-invoke design.
- Multiple `KapeAgentServer` CRs yield multiple controllers; MCP clients distinguish them by registration name (CR name) and route by the served `description`.
- Future #161 corrections should reference this ADR; the wayfinder map (#150) vocabulary ("KapeProfile reconciler", #157) is superseded in name only — `KapeAgentServer` is that reconciler's CR.
