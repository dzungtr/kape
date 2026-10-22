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
make bump-patch        # VERSION 0.3.0 -> 0.3.1
make push              # build + push ghcr.io/dzungtr/pi-openshell:<v>-<profile> and :<profile>
```

## The `openrouter` profile — provider contract + kape redirect

The image carries **no literal API key**. `profiles/openrouter/models.json`
defines the provider with `"apiKey": "$OPENROUTER_API_KEY"` — an env
reference, resolved at runtime, never a baked secret. Two ways the env gets
populated:

1. **Ad-hoc / CLI sandboxes — openshell provider contract (issue #273):**
   attach the gateway's `openrouter` provider profile; it injects the
   credential at the sandbox egress proxy, so the key never enters the
   sandbox. pi's requests to `api.openrouter.ai` get the real `Authorization`
   header added in-flight:

   ```sh
   openshell sandbox create --name pi \
     --from ghcr.io/dzungtr/pi-openshell:0.3.0-openrouter \
     --provider openrouter --no-tty -- bash -lc 'pi -p "hi"'
   ```

2. **kape-controller sandboxes — KapeAgentServer CR redirect:** pi cannot
   env-interpolate `baseUrl` (only `apiKey`/headers), so the redirect to the
   in-cluster model gateway is NOT an env var — it is a models.json patch at
   provision time, sourced from the CR:

   ```
   KapeAgentServer spec.modelGateway.url            (CR field)
     → KAPE_MODEL_GATEWAY_URL env on the controller   (operator render.go)
     → ModelGatewayURL controller config               (config.go)
     → piWrapperCommand patches providers.openrouter.baseUrl in models.json
       + injects OPENROUTER_API_KEY=dummy, PI_PROVIDER=openrouter (gateway.go)
   ```

   The model gateway is unauthenticated; the dummy Bearer it receives is
   ignored. Set `spec.modelGateway.url` to
   `http://model-gateway-http.aperture.svc.cluster.local/v1` and every
   controller-provisioned sandbox talks to the in-cluster gateway instead of
   `api.openrouter.ai`.

`sandbox-policy.yaml` allows `openrouter.ai` + `api.openrouter.ai` and the
in-cluster `model-gateway-http.aperture.svc.cluster.local:80` (L7, rest) on
the pi binary-identity set — the same shape as kape's `m1_model_gateway` rule
(`controller/policy.go`).

Note: the `aperture` profile existed in 0.2.0–0.2.2 releases and was dropped
in 0.3.0 — the KapeAgentServer redirect above replaces it. `0.2.1-aperture`
was briefly published without the load-bearing dummy key; ignore that tag.

## Policy deltas vs upstream

Each delta is marked `pi-openshell` in `sandbox-policy.yaml` — preserve them on
upstream re-sync:

- `read_only += /nix` — landlock read/exec for nix-grafted tools
- pi network binaries += `/nix/var/nix/profiles/default/bin/**`
- egress += `registry.npmjs.org` (pi extension manager)
- egress += `api.openrouter.ai` (provider-contract credential injection)
- egress += `model-gateway-http.aperture.svc.cluster.local:80` (kape redirect target)

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
