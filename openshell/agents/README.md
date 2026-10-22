# pi-openshell sandbox images

Build and release home for `ghcr.io/dzungtr/pi-openshell` — the pi coding-agent
sandbox image used by kape handlers (`kape-dev` builds `FROM` it) and ad-hoc
agent sandboxes on the homelab cluster.

Moved here from the pi host repo (`~/.pi/docker/openshell` + the vendored
`~/.pi/openshell/sandbox-policy.yaml`) so kape-io owns the source of truth and
the version (issue #273).

## Layout

| Path | What it is |
|---|---|
| `VERSION` | Image version (`0.1.1`) — tags `<semver>-<profile>` + rolling `<profile>` |
| `Dockerfile` | Two-stage build: nix `piTools` store graft onto the official OpenShell community base + pi + profile |
| `flake.nix` / `flake.lock` | Toolset closure (rg, fd, cacert, iproute2, nftables, iptables, util-linux, shadow) |
| `sandbox-policy.yaml` | Vendored upstream pi-sandbox policy + image deltas (see below) |
| `profiles/<name>/` | pi agent config swapped in via `--build-arg PROFILE=<name>` (`settings.json`, `models.json`) |
| `agent/` | Baked `/sandbox/.pi/agent` contents: `AGENTS.md`, `claude-plugins.json`, `npm/` extension deps |

## Versioning & release

```
make bump-patch        # VERSION 0.1.1 -> 0.1.2
make push              # build + push ghcr.io/dzungtr/pi-openshell:<v>-<profile> and :<profile>
```

## Provider contract (0.1.1, issue #273)

The image carries **no API key**. `profiles/openrouter/models.json` defines the
provider without `apiKey`, pointing at `https://api.openrouter.ai/api/v1`. The
credential is injected by the gateway's `openrouter` provider profile at the
sandbox egress proxy (Bearer `Authorization` header), so the key never enters
the sandbox:

```sh
openshell sandbox create --name pi \
  --from ghcr.io/dzungtr/pi-openshell:0.1.1-openrouter \
  --provider openrouter --no-tty -- bash -lc 'pi -p "hi"'
```

`sandbox-policy.yaml` allows both `openrouter.ai` and `api.openrouter.ai` on
the pi binary-identity set.

## Policy deltas vs upstream

Each delta is marked `pi-openshell` in `sandbox-policy.yaml` — preserve them on
upstream re-sync:

- `read_only += /nix` — landlock read/exec for nix-grafted tools
- pi network binaries += `/nix/var/nix/profiles/default/bin/**`
- egress += `registry.npmjs.org` (pi extension manager)
- egress += `api.openrouter.ai` (provider-contract credential injection)

**Schema note (issue #273):** `tls: terminate` and `enforcement: enforce` were
removed from the openshell v0.1.0 policy schema (TLS termination is automatic).
Carrying them makes the gateway refuse to register the embedded policy → the
supervisor falls back to the same stale file → infinite discovery loop →
sandbox wedged in `Provisioning`. Validate changes before releasing:

```sh
openshell sandbox create --name polcheck \
  --from ghcr.io/dzungtr/pi-openshell:openrouter \
  --policy ./sandbox-policy.yaml -- sleep 60   # must reach Ready
```

## Upstream sources

- Base image: `ghcr.io/nvidia/openshell-community/sandboxes/base:latest`
- Policy upstream: pi-sandbox `policy.yaml`
- Agent assets (`agent/`, `profiles/`): snapshotted from the pi host tree
  (`~/.pi`) at release time — refresh both before a release.
