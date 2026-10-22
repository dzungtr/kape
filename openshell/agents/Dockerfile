# syntax=docker/dockerfile:1.7
#
# Pi dev-agent sandbox image — OpenShell v2 (Ubuntu base + Nix store graft).
#
# Source of truth for the pi-openshell image lives HERE (moved from the pi
# host repo ~/.pi/docker/openshell; the vendored agent assets under agent/ and
# profiles/ were snapshotted from the host tree at release time).
#
# Built on the OFFICIAL OpenShell community `base` sandbox — which already
# satisfies every supervisor contract (FHS layout, /bin/bash, supervisor+sandbox
# users, /sandbox workspace) — then layers in:
#   1. pi CLI installed fresh (npm, @latest) — base carries no pi, and this
#      keeps pi reinstallable without depending on the `sandboxes/pi` image
#   2. the Nix closure from flake.nix (stage 1), copied verbatim — flake.nix
#      stays the single source of truth, consumed BY the container
#   3. the openrouter profile + vendored extension deps
#   4. a vendored sandbox policy (upstream pi-sandbox policy.yaml) patched for
#      the nix store + npm egress + provider-contract egress
#
# Versioning: the image version is tracked in ./VERSION (see Makefile).
# Tags: ghcr.io/dzungtr/pi-openshell:<semver>-<profile> and :<profile> (rolling)
#
# Provider contract (issue #273): the image carries NO API key. Credentials
# are injected by the gateway's `openrouter` provider profile at the sandbox
# egress proxy (Bearer auth header on api.openrouter.ai) — attach it at create
# time and never pass a raw key again:
#
#   openshell sandbox create --name pi-v2 \
#     --from ghcr.io/dzungtr/pi-openshell:0.1.1-openrouter \
#     --provider openrouter --no-tty -- bash -lc 'pi -p "hi"'

# --- Stage 1: build the flake toolSet into a portable /nix store -------------
# nixos/nix runs the install; /nix/store paths are absolute and identical in
# the final image, so the store can be copied verbatim between stages.
FROM docker.io/nixos/nix:latest AS nix-store

RUN printf 'experimental-features = nix-command flakes\nsandbox = false\n' >> /etc/nix/nix.conf
COPY flake.nix flake.lock /tmp/flake/
# piTools = the shared toolset (ripgrep, fd, cacert, iproute2, nftables,
# iptables, util-linux, shadow). The nixos/nix base profile already ships the
# nix CLI — it rides along in the copied store, so the container can re-run
# `nix profile install` against the same flake later (single source of truth,
# updatable at runtime).
RUN nix --extra-experimental-features "nix-command flakes" \
    profile install /tmp/flake#piTools --priority 4 \
 && rm -rf /tmp/flake /root/.cache/nix

# --- Stage 2: official OpenShell base sandbox + pi + nix store ---------------
FROM ghcr.io/nvidia/openshell-community/sandboxes/base:latest

USER root

# The community base image ships no pi; our vendored extension deps require
# pi-ai >= 0.84.x, so install pi@latest here (reinstall-friendly: bump or pin
# this one line).
RUN npm install -g @earendil-works/pi-coding-agent@latest \
 && npm cache clean --force

# Bring the nix store + default profile into the FHS image. Store paths are
# absolute (/nix/store/...) and identical in both stages, so a plain copy is
# relocatable-safe as long as the target path is also /nix. The nix CLI comes
# along inside the default profile, so the same flake can be re-consumed at
# runtime (`nix profile install /path/to/flake#piTools`).
COPY --from=nix-store /nix /nix

# Profile bins go AFTER the system PATH so Ubuntu node/git stay canonical for
# pi; nix tools (rg, fd, bash, coreutils) fill gaps without shadowing.
ENV PATH="/sandbox/.venv/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin:/nix/var/nix/profiles/default/bin:/nix/var/nix/profiles/default/sbin" \
    SSL_CERT_FILE=/nix/var/nix/profiles/default/etc/ssl/certs/ca-bundle.crt \
    NODE_EXTRA_CA_CERTS=/nix/var/nix/profiles/default/etc/ssl/certs/ca-bundle.crt \
    NIX_SSL_CERT_FILE=/nix/var/nix/profiles/default/etc/ssl/certs/ca-bundle.crt \
    NIX_PATH=/nix/var/nix/profiles/default/channels

# openrouter profile swap — defaultProvider/model + provider defs.
# Provider contract (issue #273): models.json points at
# https://api.openrouter.ai/api/v1 WITHOUT an apiKey — the gateway's
# `openrouter` provider profile injects the credential at the egress proxy.
ARG PROFILE=openrouter
COPY --chown=sandbox:sandbox profiles/${PROFILE}/settings.json /sandbox/.pi/agent/settings.json
COPY --chown=sandbox:sandbox profiles/${PROFILE}/models.json   /sandbox/.pi/agent/models.json
# Shared agent config + extension deps (installed at build so runtime stays
# offline-safe against the policy's default-deny egress). --legacy-peer-deps
# matches the host tree: the @earendil-works/pi-coding-agent peer is satisfied
# at runtime by the global pi install above, not by this tree.
COPY --chown=sandbox:sandbox agent/AGENTS.md agent/claude-plugins.json \
                /sandbox/.pi/agent/
COPY --chown=sandbox:sandbox agent/npm/package.json agent/npm/package-lock.json \
                /sandbox/.pi/agent/npm/
RUN cd /sandbox/.pi/agent/npm \
 && npm install --ignore-scripts --omit=dev --legacy-peer-deps \
 && npm cache clean --force \
 && chown -R sandbox:sandbox /sandbox/.pi/agent/npm
ENV PI_CODING_AGENT_DIR=/sandbox/.pi/agent \
    PI_OFFLINE=1

# Sandbox policy: vendored upstream pi-sandbox policy with the image-specific
# deltas already baked in (each marked "pi-openshell" in the file — preserve
# them on upstream re-sync):
#   - read_only += /nix              (landlock: sandbox children exec nix tools)
#   - pi binaries   += nix glob      (network policy binary identity)
#   - egress    += registry.npmjs.org (pi's extension manager installs at runtime)
#   - egress    += api.openrouter.ai  (provider-contract credential injection)
#   - tls/enforcement fields REMOVED  (openshell v0.1.0 schema; issue #273 —
#     stale fields make the gateway reject the policy and wedge the sandbox)
# COPY defaults to root:root, matching the upstream policy's expected owner.
COPY sandbox-policy.yaml /etc/openshell/policy.yaml

USER sandbox
WORKDIR /sandbox

# No ENTRYPOINT override: the base image's supervisor-compatible setup is
# already driver-managed (the podman driver replaces the entrypoint).
