# Phase 8.6 — mTLS for NATS

**Status:** Draft
**Date:** 2026-05-17
**GitHub Issue:** #81
**Phase:** 08-audit-security
**Milestone:** M4
**Reference Specs:** 0006
**Depends on:** #76 (K8s Audit Adapter — creates `adapters/cmd/audit/main.go`)

---

## Goal

Enforce mutual TLS on all NATS connections in kape-io. Every adapter and the handler runtime must present a valid client certificate signed by the kape-ca CA. Non-mTLS connections are rejected at the NATS server level. cert-manager handles certificate issuance and rotation automatically.

---

## Background

Currently all NATS connections in kape-io are plain TCP — no encryption, no authentication at the transport layer. Any workload that can reach the NATS service endpoint on port 4222 can publish to `kape.events.>` or subscribe to all events.

mTLS closes two threats:

1. **Unauthenticated publishers:** a compromised workload in `kape-system` could inject arbitrary events into the KAPE pipeline, triggering handler actions (webhook calls, cluster mutations, chained events).
2. **Unauthenticated subscribers:** any pod that can resolve `nats.kape-system.svc` can subscribe to all `kape.events.>` subjects, receiving every security signal that passes through the broker.

With mTLS + CN-based NATS authorization (spec 0006, section 3), connections without a valid certificate signed by `kape-ca` are rejected at the TLS handshake. After the handshake, the NATS server maps the client certificate CN to a permission set — adapters get publish-only, handlers get subscribe + publish.

---

## Architecture

### Certificate Hierarchy

```
kape-ca (ClusterIssuer, self-signed)
├── kape-adapter-cert   CN: kape-adapter
│     Issued to:  all adapter Deployments
│     NATS permissions: publish to kape.events.> only
│                       subscribe: deny all
│
└── kape-handler-cert   CN: kape-handler
      Issued to:  all handler Pods (injected by operator)
      NATS permissions: subscribe to kape.events.>
                        publish to kape.events.>
```

Two client certificate resources are created (not one per adapter). All adapter Deployments share the same `kape-adapter-cert` Secret. All handler Pods share the same `kape-handler-cert` Secret. cert-manager rotates both automatically before expiry.

The NATS server itself also gets a certificate from `kape-ca` for its server-side TLS. Clients verify this certificate against the same CA, establishing full mutual trust.

### Connection flow

```
Adapter pod                NATS Server                 Handler pod
   │                           │                            │
   │── TLS ClientHello ────────►                            │
   │◄── TLS ServerHello + server cert (CN: nats-server) ───┤
   │── client cert (CN: kape-adapter) ─────────────────────►
   │   NATS maps CN → publish-only permissions              │
   │                           │◄── client cert (CN: kape-handler)
   │                           │    NATS maps CN → subscribe+publish
```

---

## NATS Server Config Changes

Update the NATS configuration (in `helm/templates/nats.yaml` or the equivalent `examples/nats/nats.yaml` ConfigMap) to add a `tls` block and an `authorization` block.

### TLS block

```yaml
tls:
  ca_file: /etc/nats/certs/ca.crt
  cert_file: /etc/nats/certs/tls.crt
  key_file: /etc/nats/certs/tls.key
  verify: true          # require client certificate on every connection
  verify_and_map: true  # map client cert CN to authorization permission set
```

### Authorization block

```yaml
authorization:
  users:
    - user: kape-adapter
      permissions:
        publish:
          allow: ["kape.events.>"]
        subscribe:
          deny: [">"]
    - user: kape-handler
      permissions:
        publish:
          allow: ["kape.events.>"]
        subscribe:
          allow: ["kape.events.>"]
```

The `user` field under `verify_and_map` is matched against the client certificate CN. The CN values `kape-adapter` and `kape-handler` must exactly match the CN set in the cert-manager `Certificate` manifests.

### NATS server certificate volume

The NATS StatefulSet needs a volume mounting the NATS server certificate Secret (`kape-nats-server-cert`) at `/etc/nats/certs/`. Add or update:

```yaml
# In the NATS StatefulSet pod spec:
volumeMounts:
  - name: nats-server-tls
    mountPath: /etc/nats/certs
    readOnly: true

volumes:
  - name: nats-server-tls
    secret:
      secretName: kape-nats-server-cert
```

---

## Certificate Manifests

All four manifests live in `examples/certs/`. Apply them in order: `issuer.yaml` first (the others depend on it).

### `examples/certs/issuer.yaml`

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: kape-ca
spec:
  selfSigned: {}
---
# Self-signed CA certificate — ClusterIssuer signs itself, then becomes the
# issuer for all kape-* certificates below.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-ca
  namespace: cert-manager
spec:
  isCA: true
  commonName: kape-ca
  secretName: kape-ca-secret
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: kape-ca
    kind: ClusterIssuer
    group: cert-manager.io
---
# ClusterIssuer backed by the CA certificate above
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: kape-ca-issuer
spec:
  ca:
    secretName: kape-ca-secret
```

> Note: the bootstrap issuer (`kape-ca`, selfSigned) is only used to sign the CA certificate itself. All subsequent certificates reference `kape-ca-issuer`.

### `examples/certs/nats-server-cert.yaml`

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-nats-server-cert
  namespace: kape-system
spec:
  secretName: kape-nats-server-cert
  commonName: nats-server
  dnsNames:
    - nats.kape-system.svc
    - nats.kape-system.svc.cluster.local
    - "*.nats-headless.kape-system.svc.cluster.local"  # StatefulSet pod DNS
  duration: 8760h    # 1 year
  renewBefore: 720h  # renew 30 days before expiry
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: kape-ca-issuer
    kind: ClusterIssuer
    group: cert-manager.io
```

### `examples/certs/nats-client-adapter-cert.yaml`

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-adapter-cert
  namespace: kape-system
spec:
  secretName: kape-adapter-cert
  commonName: kape-adapter     # MUST match authorization.users[].user in NATS config
  duration: 8760h
  renewBefore: 720h
  usages:
    - client auth
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: kape-ca-issuer
    kind: ClusterIssuer
    group: cert-manager.io
```

### `examples/certs/nats-client-handler-cert.yaml`

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-handler-cert
  namespace: kape-system
spec:
  secretName: kape-handler-cert
  commonName: kape-handler     # MUST match authorization.users[].user in NATS config
  duration: 8760h
  renewBefore: 720h
  usages:
    - client auth
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: kape-ca-issuer
    kind: ClusterIssuer
    group: cert-manager.io
```

---

## Go Adapter Changes

### Dependency note

`adapters/cmd/audit/main.go` does not exist until issue #76 (K8s Audit Adapter) is merged. This issue (#81) MUST be implemented after #76. Both `main.go` files receive identical TLS wiring.

### Shared helper: `adapters/internal/nats/connect.go`

Extract the TLS connection logic into a new package-level helper so both adapters share one implementation:

```go
package nats

import (
    "crypto/tls"
    "crypto/x509"
    "fmt"
    "os"
    "time"

    natsgo "github.com/nats-io/nats.go"
    "github.com/rs/zerolog/log"
)

// Connect establishes a NATS connection. If certFile, keyFile, and caFile are
// all non-empty, mTLS is configured using those paths. If any of the three
// values is empty, the connection is made without TLS and a warning is logged
// (local dev / CI fallback only — production deployments must set all three).
func Connect(url, name, certFile, keyFile, caFile string) (*natsgo.Conn, error) {
    opts := []natsgo.Option{
        natsgo.Name(name),
        natsgo.MaxReconnects(-1),
        natsgo.ReconnectWait(2 * time.Second),
    }

    if certFile != "" && keyFile != "" && caFile != "" {
        tlsCfg, err := buildTLSConfig(certFile, keyFile, caFile)
        if err != nil {
            return nil, fmt.Errorf("building TLS config: %w", err)
        }
        opts = append(opts, natsgo.Secure(tlsCfg))
        log.Info().
            Str("cert", certFile).
            Str("ca", caFile).
            Msg("mTLS enabled for NATS connection")
    } else {
        log.Warn().Msg("NATS_TLS_CERT / NATS_TLS_KEY / NATS_TLS_CA not set — connecting without mTLS (local dev only)")
    }

    return natsgo.Connect(url, opts...)
}

func buildTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
    cert, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        return nil, fmt.Errorf("loading client key pair: %w", err)
    }

    caPEM, err := os.ReadFile(caFile)
    if err != nil {
        return nil, fmt.Errorf("reading CA cert %s: %w", caFile, err)
    }
    caPool := x509.NewCertPool()
    if !caPool.AppendCertsFromPEM(caPEM) {
        return nil, fmt.Errorf("parsing CA cert from %s", caFile)
    }

    return &tls.Config{
        Certificates: []tls.Certificate{cert},
        RootCAs:      caPool,
        MinVersion:   tls.VersionTLS13,
    }, nil
}
```

### `adapters/cmd/alertmanager/main.go` — updated connection block

Replace the current plain `natsgo.Connect(...)` call with the shared helper:

```go
// Read TLS env vars — all three must be set to enable mTLS
tlsCert := os.Getenv("NATS_TLS_CERT")
tlsKey  := os.Getenv("NATS_TLS_KEY")
tlsCA   := os.Getenv("NATS_TLS_CA")

nc, err := natspkg.Connect(natsURL, "kape-alertmanager-adapter", tlsCert, tlsKey, tlsCA)
if err != nil {
    log.Fatal().Err(err).Str("nats_url", natsURL).Msg("failed to connect to NATS")
}
defer nc.Drain()
log.Info().Str("nats_url", natsURL).Msg("connected to NATS")
```

The import block already has `natspkg "github.com/kape-io/kape/adapters/internal/nats"`. No new import is needed — `Connect` is now exported from that package.

### `adapters/cmd/audit/main.go` — same pattern

After #76 creates this file, apply the identical env var block and helper call:

```go
tlsCert := os.Getenv("NATS_TLS_CERT")
tlsKey  := os.Getenv("NATS_TLS_KEY")
tlsCA   := os.Getenv("NATS_TLS_CA")

nc, err := natspkg.Connect(natsURL, "kape-audit-adapter", tlsCert, tlsKey, tlsCA)
if err != nil {
    log.Fatal().Err(err).Str("nats_url", natsURL).Msg("failed to connect to NATS")
}
defer nc.Drain()
```

### `adapters/internal/nats/publisher.go` — no changes

`publisher.go` accepts an already-established `*natsgo.Conn`. TLS is a connection-level concern resolved before `NewPublisher` is called. This file is not modified.

### Adapter Deployment manifest changes

Each adapter Deployment (`kape-alertmanager-adapter`, `kape-audit-adapter`) needs the cert volume and three env vars added:

```yaml
env:
  # ... existing env vars ...
  - name: NATS_TLS_CERT
    value: /etc/kape/nats-certs/tls.crt
  - name: NATS_TLS_KEY
    value: /etc/kape/nats-certs/tls.key
  - name: NATS_TLS_CA
    value: /etc/kape/nats-certs/ca.crt

volumeMounts:
  - name: nats-certs
    mountPath: /etc/kape/nats-certs
    readOnly: true

volumes:
  - name: nats-certs
    secret:
      secretName: kape-adapter-cert
```

---

## Python Consumer Changes

### `runtime/src/kape_runtime/consumer.py`

The `ConsumerLoop.run()` method currently calls `await nats.connect(nats_cfg.url)` with no TLS. Replace with a helper that reads the env vars and conditionally enables TLS.

Add the helper function near the top of the file (after imports):

```python
import os
import ssl

def _build_nats_tls_context() -> ssl.SSLContext | None:
    """
    Build an SSL context for mTLS NATS connections.

    Returns an SSLContext if NATS_TLS_CERT, NATS_TLS_KEY, and NATS_TLS_CA are
    all set. Returns None if any variable is absent — caller connects without TLS
    (local dev / CI only).
    """
    cert_file = os.environ.get("NATS_TLS_CERT")
    key_file  = os.environ.get("NATS_TLS_KEY")
    ca_file   = os.environ.get("NATS_TLS_CA")

    if not all([cert_file, key_file, ca_file]):
        logger.warning(
            "NATS_TLS_CERT / NATS_TLS_KEY / NATS_TLS_CA not set — "
            "connecting to NATS without mTLS (local dev only)"
        )
        return None

    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.load_verify_locations(cafile=ca_file)
    ctx.load_cert_chain(certfile=cert_file, keyfile=key_file)
    logger.info(
        "mTLS enabled for NATS connection (cert=%s ca=%s)", cert_file, ca_file
    )
    return ctx
```

Update `ConsumerLoop.run()` to use the helper:

```python
async def run(self, nats_cfg: NatsConfig) -> None:
    """Connect to NATS and run the pull consumer loop indefinitely."""
    tls_ctx = _build_nats_tls_context()

    connect_kwargs: dict = {"servers": [nats_cfg.url]}
    if tls_ctx is not None:
        connect_kwargs["tls"] = tls_ctx

    nc = await nats.connect(**connect_kwargs)
    self._nc = nc
    js = nc.jetstream()
    sub = await js.pull_subscribe(
        subject=nats_cfg.subject,
        durable=nats_cfg.consumer,
        stream=nats_cfg.stream,
    )
    logger.info(
        "NATS consumer started: subject=%s consumer=%s",
        nats_cfg.subject,
        nats_cfg.consumer,
    )

    try:
        while True:
            try:
                msgs = await sub.fetch(1, timeout=5.0)
                for msg in msgs:
                    await self.process_message(msg)
            except nats.errors.TimeoutError:
                continue
    finally:
        await nc.drain()
```

The `ssl` and `os` modules are part of the Python standard library — no new dependencies.

---

## Operator Changes

Handler Pods are created by the KAPE operator in `operator/infra/k8s/deployment.go`. The operator must inject the `kape-handler-cert` Secret as a volume into every handler Deployment it creates.

### Changes to `operator/infra/k8s/deployment.go`

When building the handler Deployment spec, add:

1. A volume referencing the `kape-handler-cert` Secret.
2. A volume mount for the handler container at `/etc/kape/nats-certs`.
3. Three environment variables (`NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_CA`) pointing to the mounted paths.

Conceptually (exact field names follow the existing pattern in `deployment.go`):

```go
// Volume
corev1.Volume{
    Name: "nats-certs",
    VolumeSource: corev1.VolumeSource{
        Secret: &corev1.SecretVolumeSource{
            SecretName: "kape-handler-cert",
        },
    },
},

// VolumeMount (on the handler container)
corev1.VolumeMount{
    Name:      "nats-certs",
    MountPath: "/etc/kape/nats-certs",
    ReadOnly:  true,
},

// Env vars (on the handler container)
corev1.EnvVar{Name: "NATS_TLS_CERT", Value: "/etc/kape/nats-certs/tls.crt"},
corev1.EnvVar{Name: "NATS_TLS_KEY",  Value: "/etc/kape/nats-certs/tls.key"},
corev1.EnvVar{Name: "NATS_TLS_CA",   Value: "/etc/kape/nats-certs/ca.crt"},
```

The Secret name `kape-handler-cert` is a cluster-scoped constant (it is the `secretName` in `nats-client-handler-cert.yaml`). It can be a named constant in the operator codebase rather than a hard-coded string.

---

## Design Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | Two client certs (`kape-adapter-cert`, `kape-handler-cert`) rather than one shared cert | Enables CN-based NATS authorization — adapters get publish-only, handlers get subscribe+publish. A single shared cert cannot express this permission split. Matches spec 0006 section 3. |
| 2 | Shared `Connect()` helper in `adapters/internal/nats/connect.go` | Both adapter binaries need identical TLS wiring. One implementation eliminates drift. `publisher.go` is unchanged — TLS is a connection concern, not a publish concern. |
| 3 | Env vars (`NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_CA`) rather than a mounted config file | Consistent with existing env var convention in adapters. Kubernetes populates the paths via `volumeMounts`; env vars point to those paths. Works the same way in Go and Python. |
| 4 | Graceful fallback when env vars are absent | Local dev and CI environments run without cert-manager. The warning makes the non-mTLS case visible in logs without breaking `go run` or `pytest` workflows. |
| 5 | TLS 1.3 minimum (`tls.VersionTLS13` in Go, `ssl.TLSVersion.TLSv1_3` in Python) | Eliminates legacy cipher suites. NATS server and Go/Python TLS libraries all support 1.3. |
| 6 | ECDSA P-256 keys | Smaller key size than RSA-2048 with equivalent security. Faster TLS handshake, lower memory overhead, suitable for high-frequency adapter connections. |
| 7 | `verify_and_map: true` in NATS config | Required to activate CN-to-permission mapping. Without this flag, mTLS is enforced but all authenticated clients have the same (unrestricted) permissions. |
| 8 | Wave 2 sequencing: #81 after #76 | `adapters/cmd/audit/main.go` does not exist until #76 is merged. Implementing #81 before #76 would require a stub file or a partial PR that cannot be tested end-to-end. |

---

## Key Files

| File | Change |
|------|--------|
| `examples/certs/issuer.yaml` | New — ClusterIssuer (self-signed bootstrap) + CA Certificate + kape-ca-issuer |
| `examples/certs/nats-server-cert.yaml` | New — NATS server TLS certificate |
| `examples/certs/nats-client-adapter-cert.yaml` | New — adapter mTLS client certificate (CN: kape-adapter) |
| `examples/certs/nats-client-handler-cert.yaml` | New — handler mTLS client certificate (CN: kape-handler) |
| `adapters/internal/nats/connect.go` | New — shared `Connect()` helper with mTLS support |
| `adapters/cmd/alertmanager/main.go` | Modified — replace plain `natsgo.Connect` with `natspkg.Connect` + TLS env vars |
| `adapters/cmd/audit/main.go` | Modified (after #76) — same TLS wiring as alertmanager |
| `runtime/src/kape_runtime/consumer.py` | Modified — add `_build_nats_tls_context()` + update `run()` |
| `operator/infra/k8s/deployment.go` | Modified — inject `kape-handler-cert` volume + env vars into handler Deployments |
| `helm/templates/nats.yaml` (or `examples/nats/nats.yaml`) | Modified — add `tls` block, `authorization` block, server cert volume mount |

---

## Acceptance Criteria

1. A plain `nats sub kape.events.>` client (no TLS) is rejected with a TLS handshake error.
2. A client presenting a certificate NOT signed by `kape-ca` is rejected.
3. A client presenting `kape-adapter-cert` can publish to `kape.events.security.falco` and receives a permissions error if it attempts to subscribe.
4. A client presenting `kape-handler-cert` can subscribe to `kape.events.>` and publish to `kape.events.>`.
5. The alertmanager adapter starts successfully when `NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_CA` are set, connects with mTLS, and publishes a CloudEvent that the runtime consumer receives.
6. The audit adapter (post-#76) starts with mTLS using the same env vars and publishes to `kape.events.security.audit`.
7. The Python runtime consumer connects with mTLS when env vars are set and receives events published by adapters.
8. When env vars are absent, adapters and consumer log a warning and connect without TLS (local dev fallback — server must also be in non-TLS mode for this to work).
9. cert-manager Certificate resources reach `Ready=True` state in a kind cluster with cert-manager installed.

---

## Testing Strategy

### Unit tests

- `adapters/internal/nats/connect_test.go`: test `buildTLSConfig` with valid cert/key/CA files (generate self-signed test certs in `testdata/`). Test that an empty cert path returns `nil, nil` (no TLS) and that a missing file returns an error.
- `runtime/tests/test_consumer_tls.py`: test `_build_nats_tls_context()` — verify it returns `None` when env vars are absent, verify it returns an `ssl.SSLContext` when valid paths are provided (use `tmp_path` + `trustme` or `cryptography` to generate test certs).

### Integration tests

Run against a local NATS server started with TLS enabled (use `nats-server -c testdata/nats-mtls.conf` in the test harness):

1. Start NATS with `verify: true` and the test CA.
2. Assert that a plain connection attempt raises `nats.errors.NoServersError` (TLS required).
3. Start the alertmanager adapter with valid TLS env vars pointing to test certs.
4. POST a sample AlertManager payload.
5. Subscribe as a handler (using `kape-handler-cert` equivalent test cert) and assert the CloudEvent arrives.

### Kind cluster smoke test (manual, pre-merge)

```bash
# 1. Install cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.0/cert-manager.yaml

# 2. Apply cert manifests
kubectl apply -f examples/certs/issuer.yaml
kubectl apply -f examples/certs/nats-server-cert.yaml
kubectl apply -f examples/certs/nats-client-adapter-cert.yaml
kubectl apply -f examples/certs/nats-client-handler-cert.yaml

# 3. Verify all certs are Ready
kubectl get certificates -n kape-system

# 4. Deploy NATS with TLS config
# (helm upgrade or kubectl apply the updated nats manifest)

# 5. Attempt unauthenticated connection — must be rejected
kubectl run nats-plain-test --image=natsio/nats-box --restart=Never -- \
  nats sub --server nats://nats.kape-system.svc:4222 "kape.events.>"
# Expected: TLS error in pod logs

# 6. Deploy adapters — observe mTLS connection in logs
# Expected log line: "mTLS enabled for NATS connection"

# 7. POST sample event to alertmanager adapter
# 8. Observe event received by handler runtime over mTLS
```
