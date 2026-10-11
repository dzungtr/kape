# Upstream pins & known gaps — agent-sandbox, OpenShell (issue #163)

Every upstream fact was verified against GitHub (REST API — authoritative for releases/PRs/issues)
on **2026-10-11**. Repo-local facts cite `controller/README.md` in this worktree. Anything not
confirmed against a primary source is explicitly marked `UNVERIFIED`. Companion research log:
subagent artifact `research.md` (session 09007681).

## Pinned versions

| Component | Pinned version | First release containing needed feature | Source | Retrieved |
|---|---|---|---|---|
| OpenShell gateway — kape controller-v1 (current) | v0.0.116 (2026-08-28) | suspend/resume + stop/start lifecycle (PR #2653, merged 2026-08-13) predates the pin, so contained — but the exact first-containing OpenShell release is `UNVERIFIED` (release notes don't itemize; candidates v0.0.109–v0.0.116) | `controller/README.md` (kape); https://github.com/NVIDIA/OpenShell/releases | 2026-10-11 |
| OpenShell gateway — recommended M2 pin | v0.1.3 (2026-10-09) | provider-profiles-authoritative (#2962) in v0.1.0; HTTP profile import (#3706, 2026-09-25); applied-provider-change reporting (#3391, 2026-09-17); on-demand sandbox config files (#3832) in v0.1.3 | https://github.com/NVIDIA/OpenShell/releases ; /pull/2962 ; /pull/3706 ; /pull/3391 ; /pull/3832 | 2026-10-11 |
| agent-sandbox — OpenShell e2e default | v1.0.3 | — (upstream e2e default install) | https://github.com/NVIDIA/OpenShell/blob/main/e2e/support/install-agent-sandbox.sh | 2026-10-11 |
| agent-sandbox — recommended M2 pin | v1.0.6 (2026-10-08) | `SandboxClaim` labels+selectors and `--sandbox-claim-warm-candidate-grace-period` flag (Epic 3 #192 needs) | https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.6 | 2026-10-11 |
| agent-sandbox extension CRDs (`SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`) | shipped since **v0.1.0 (2025-11-07)** | v0.1.0 itself | https://github.com/kubernetes-sigs/agent-sandbox/releases | 2026-10-11 |

API groups (from v1.0.0 release notes): core `sandboxes.agents.x-k8s.io`; extension
`sandboxclaims` / `sandboxtemplates` / `sandboxwarmpools.extensions.agents.x-k8s.io`.
Source: https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0 (2026-10-11).

## Known gaps (upstream issues → M2 impact)

### Warm pools are agent-sandbox-native, NOT OpenShell (correction to map #150)

- OpenShell PR #2460 "create SandboxClaims for matching warm pools" — **closed, unmerged**
  (review flagged missing `networkPolicyManagement` on the `SandboxTemplate`; never merged).
  https://github.com/NVIDIA/OpenShell/pull/2460 (2026-10-11).
- OpenShell PR #1892 "warm-pooled sandboxes via agent-sandbox extension CRDs" — **closed, unmerged** (2026-08-11).
  https://github.com/NVIDIA/OpenShell/pull/1892 (2026-10-11).
- OpenShell issue #1879 "Warm-pooled sandboxes for the Kubernetes compute driver" — **closed, not_planned** (2026-09-21).
  https://github.com/NVIDIA/OpenShell/issues/1879 (2026-10-11).
- **Impact on M2 (map #150):** map #150/#158's premise "requires OpenShell ≥ PR #2460" is **stale** —
  no OpenShell release contains it. M2 must treat warm pooling as agent-sandbox extension CRDs
  (`SandboxWarmPool`/`SandboxClaim`); OpenShell consumes warm candidates via its agent-sandbox
  install and owns no pooling logic.
- **Workaround/design:** drive `SandboxWarmPool` + `SandboxClaim` directly (v1.0.6 gives labels,
  selectors, and the warm-candidate grace flag with cold-create fallback, default 2s).

### Suspend/resume — #2035 resolved via #2652/#2653

- OpenShell issue #2652 "add storage-preserving suspend and resume lifecycle" — **closed completed 2026-08-13**;
  PR #2653 "feat(sandbox): add stop and start operations" — **merged 2026-08-13**.
  https://github.com/NVIDIA/OpenShell/issues/2652 ; /pull/2653 (2026-10-11).
- OpenShell issue #2035 ("expose agent-sandbox `operatingMode` for idle scale-to-zero with PVC retention")
  — **closed 2026-09-21**, cross-referenced to #2652. https://github.com/NVIDIA/OpenShell/issues/2035 (2026-10-11).
- **Impact on M2:** storage-preserving suspend/resume + stop/start landed 2026-08-13, before kape's
  v0.0.116 pin (2026-08-28), so the pinned gateway contains it. Exact first-containing release
  `UNVERIFIED` (see table).

### #3470 — manifest asset bug, OPEN

- **Open**, no fix release. OpenShell's k8s setup guide references a nonexistent agent-sandbox
  `manifest.yaml`. https://github.com/NVIDIA/OpenShell/issues/3470 (2026-10-11).
- **Cause:** agent-sandbox renamed the core release manifest after v0.5.1 — `manifest.yaml` for
  ≤v0.5.1, `sandbox.yaml` for ≥v0.5.2. Workaround is upstream's own
  `e2e/support/install-agent-sandbox.sh`: install from
  `https://github.com/kubernetes-sigs/agent-sandbox/releases/download/<tag>/<name>` with the
  correct name per tag, or use the script.
- **Impact on M2:** kape install automation must select the manifest name per agent-sandbox tag;
  do not copy the setup guide's stale asset name.

### #1886 — declarative gateway.toml, closed not_planned

- **closed not_planned (2026-09-21)** — will not ship. https://github.com/NVIDIA/OpenShell/issues/1886 (2026-10-11).
- **Shipped direction instead:** provider profiles — #2962 "make profiles authoritative" merged
  2026-09-01 (in v0.1.0); #3706 import profiles from HTTP URLs (2026-09-25); #3391 report applied
  provider changes (2026-09-17); #3832 serve sandbox config files on demand (2026-09-30, in v0.1.3).
  Profiles + Helm values are the config surface.
- **Impact on M2:** map #157's "watch #1886 to shrink the shim" is resolved — design the shim
  against provider profiles + Helm values, not a declarative gateway.toml.

## Compatibility — pin pairs

| OpenShell | agent-sandbox | Basis |
|---|---|---|
| v0.0.116 (kape current) | e2e default v1.0.3 | gateway auto-detects served Agent Sandbox API versions per process; restart gateway after an agent-sandbox CRD/controller upgrade — https://github.com/NVIDIA/OpenShell/blob/main/docs/kubernetes/setup.mdx (2026-10-11) |
| v0.1.3 (recommended M2) | v1.0.6 (recommended M2) | **Recommended, not decided** — final pin decision belongs to M2 implementation. v1.0.6 over the e2e-default v1.0.3 because Epic 3 (#192, revive) needs `SandboxClaim` labels/selectors + the warm-candidate grace flag. |

Full per-version compatibility matrix beyond these pairs: `UNVERIFIED` (upstream publishes no
matrix; only the auto-detect + restart rule and the e2e default are documented).

Upgrade order when changing pins (setup.mdx): agent-sandbox CRD rollout → gateway restart
(gateway caches served API versions per process).

agent-sandbox v1.0.0 (2026-08-28) caveat: core+extension APIs are exclusively `v1beta1`
(`v1alpha1` removed); direct upgrade `<v0.5.0 → v1.0.0` is unsupported — 4-step migration in the
release notes. https://github.com/kubernetes-sigs/agent-sandbox/releases/tag/v1.0.0 (2026-10-11).

## Item 7 — what kape's v0.0.116 pin contains vs M2 needs

- Repo-local (verified in `controller/README.md`): gateway **v0.0.116** deployed; protos under
  `controller/proto/` copied from `github.com/NVIDIA/OpenShell` at tag **v0.0.116** — main-branch
  protos drifted (`WorkspaceSelector` oneofs, `ExecSandboxRequest.sandbox` don't match the
  deployed server). Sandbox name cap ≤19 chars verified live for #179.
- Contains (by date): suspend/resume + stop/start (landed 2026-08-13 < pin date 2026-08-28).
- Does **not** contain: provider-profiles-authoritative config (#2962, in v0.1.0) or #3832's
  on-demand config-file serving (v0.1.3) — both post-date v0.0.116.
- Daemon/supervisor hooks in v0.0.116 specifically: `UNVERIFIED` (release notes don't itemize;
  M2's bridge daemon is kape-side per `controller/README.md`).

## Open questions

- Exact first OpenShell release containing #2652/#2653 (candidates v0.0.109–v0.0.116) — needs a
  per-release-notes or bisect pass. Marked `UNVERIFIED` above.
