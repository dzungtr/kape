# Runtime credential injection via OpenShell NetworkCredentialBinding (operator-transit)

## Status

accepted

## Context

Sandbox agents needed runtime credentials (git/gh first), but `CreateProfile` carries no env/secret field and credentials were baked into the sandbox image — rotation meant rebuild (#260). Four options were weighed (issue #260 design space): OpenShell `NetworkCredentialBinding`, caller-supplied env on `CreateProfile`, file-mount injection (ADR-0065 pattern), and status quo. Two upstream facts (verified, not assumed) bound the choice: NVIDIA/OpenShell #1678 (Secret-ref provider credentials) is closed `NOT_PLANNED`, so gateway-resolved k8s Secrets will never exist; and provider profiles are authoritative for new providers since NVIDIA/OpenShell #2962 (OpenShell v0.1.0), which also retires legacy `generic` provider creation.

## Decision

Credentials are injected at the OpenShell **egress proxy** via `NetworkCredentialBinding`: secret material never enters sandbox env or filesystem, preserving the untrusted-sandbox posture. The flow is **operator-transit**: the operator reads same-namespace, label-gated (`kape.io/credential=true`) k8s Secrets (get-only RBAC, values in-memory only, never logged) and pushes them into OpenShell provider records via the v0.1.0 spec API (`ImportProviderProfiles`, `CreateProvider`/`UpdateProvider`, `RotateProviderCredential`). The controller merges `NetworkEndpoint{credential_binding}` into each sandbox policy from an env-fed binding table (`KAPE_CREDENTIAL_BINDINGS`) via the existing `ApplyModelGatewayPolicy` seam, `on_error: fail_closed`, with calling binaries allowlisted on bound endpoints. Failures are **degrade-loud**: per-credential status conditions; only successfully-pushed providers reach the env; a broken credential fails at use time, never blocking the healthy profile. Rotation = edit the Secret; reconcile re-pushes. Scoped to OpenShell v0.1.0; sandboxes stay in the shared driver namespace (workspace-per-profile, shipped upstream in #2656, is the documented deferral to #162).

Considered and rejected: caller env (weakest posture — env is visible to every sandbox process, contradicting ADR-0065's rationale); file mounts (kept as fallback for credentials that cannot ride a proxy, e.g. git-over-SSH); waiting on upstream Secret-refs (#1678 is `NOT_PLANNED`).

## Consequences

- The operator becomes a **secret-transit** component — its threat model and RBAC must be reviewed as such.
- LLM-key injection is explicitly untouched: it stays on the model-gateway path.
- The v0.1.0 upgrade and controller proto re-pin are hard prerequisites (coordinate #163); any script using `openshell provider create --type generic` breaks on v0.1.0 and needs profile-backed equivalents.
- New credential classes (registries, MCP-over-HTTP) are registry config, not code.
- Rotation loses its image-rebuild cost; the sandbox image's baked `GITHUB_TOKEN` becomes dead weight, removed as decoupled hygiene.
