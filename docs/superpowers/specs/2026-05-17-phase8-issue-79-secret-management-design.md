# Phase 8.4 — Secret Management

**Status:** Draft
**Date:** 2026-05-17
**GitHub Issue:** #79
**Phase:** 08-audit-security
**Milestone:** M4
**Reference Specs:** 0007

---

## Goal

Replace environment-variable injection of KapeTool connection secrets with file
mounts in the handler Deployment, and provide External Secrets Operator (ESO)
reference manifests so teams can source those secrets from an external vault rather
than hand-crafting Kubernetes Secrets.

---

## Background (why file mounts over env vars)

Environment variables in Kubernetes are accessible to every process in a container,
survive `exec` into the pod unfiltered, and appear in process listings and crash
dumps. The KAPE security model (spec 0007) already classifies the handler pod as an
untrusted boundary: the kapeproxy sidecar enforces an `allowedTools` allowlist
(Layer 2) precisely because the handler runtime cannot be unconditionally trusted.
Exposing Qdrant credentials as env vars contradicts this stance.

The specific concerns are:

1. **LLM exfiltration surface.** The handler runtime passes credentials to
   `langchain_qdrant`. If prompt injection (Layer 4 threat) causes the LLM to emit
   the value of `QDRANT_URL` or `QDRANT_COLLECTION` inside a tool call argument, the
   kapeproxy sidecar's output redaction (Layer 3) has no visibility into env vars —
   only into the tool call payload. A file at `/etc/kape/secrets/…` that is never
   exposed to the LLM's input path has a smaller exfiltration surface.

2. **Audit log integrity (Layer 7).** Spec 0007 §9 notes that `$.spec.containers[*].env`
   is one of the default redaction rules for k8s-mcp-read output. The operator
   injecting secrets as env vars means they exist in a location the audit redaction
   system was specifically designed to suppress from LLM visibility — a contradiction
   that erodes the redaction rationale.

3. **ESO integration readiness.** External Secrets Operator populates Kubernetes
   `Secret` objects from a backend vault (Vault, AWS Secrets Manager, GCP Secret
   Manager). The natural way to surface a `Secret` to a pod is as a file mount, not
   as `envFrom`. File mounts support per-key granularity, are readable at init time,
   and decouple the secret lifecycle from the pod env.

4. **Least privilege.** A file mounted read-only at a known path is easier to reason
   about and audit than an env var that any subprocess can read.

---

## Architecture

The change has three independent parts. Each part can be reviewed and landed
separately, but all three must be deployed together before the env vars can be
removed from production.

```
Part A — ESO manifests (examples/eso/)
  New reference manifests only. No operator code change.
  Operators apply these to their cluster if they use ESO.

Part B — Operator file mounts (operator/infra/k8s/deployment.go)
  buildDeployment() adds a Volume + VolumeMount per memory-type KapeTool.
  Env var injection for QDRANT_URL / QDRANT_COLLECTION is removed.
  The Secret naming convention kape-tool-<tool-name>-conn is established here.

Part C — Runtime file reads (runtime/src/kape_runtime/memory.py)
  build_memory_tool() reads from /etc/kape/secrets/<tool-name>/qdrant_url
  and /etc/kape/secrets/<tool-name>/qdrant_collection.
  Falls back to QDRANT_URL / QDRANT_COLLECTION env vars if file does not exist
  (local development path).
  KAPE_SECRETS_DIR env var configures the base path (default /etc/kape/secrets).
```

### Secret and volume naming

| Item | Pattern | Example |
|---|---|---|
| Secret name | `kape-tool-<tool-name>-conn` | `kape-tool-order-memory-conn` |
| Volume name | `kape-tool-<tool-name>-secrets` | `kape-tool-order-memory-secrets` |
| Mount path | `/etc/kape/secrets/<tool-name>` | `/etc/kape/secrets/order-memory` |
| Key: URL | `qdrant_url` | file: `…/qdrant_url` |
| Key: collection | `qdrant_collection` | file: `…/qdrant_collection` |

The base secrets directory is configurable via `KAPE_SECRETS_DIR` (default:
`/etc/kape/secrets`). This allows tests to point at a temp directory.

### Fallback read order (Part C)

```
1. Compute secrets_dir = os.environ.get("KAPE_SECRETS_DIR", "/etc/kape/secrets")
2. Compute tool_name = os.environ.get("KAPE_TOOL_NAME")   # injected by operator
3. If tool_name is set:
     url_path   = secrets_dir / tool_name / "qdrant_url"
     coll_path  = secrets_dir / tool_name / "qdrant_collection"
     If url_path exists: read url from file
     Else: url = os.environ.get("QDRANT_URL")
     If coll_path exists: read collection from file
     Else: collection = os.environ.get("QDRANT_COLLECTION")
4. If tool_name is not set (legacy / unmanaged deployment):
     url = os.environ.get("QDRANT_URL")
     collection = os.environ.get("QDRANT_COLLECTION")
5. If url or collection is still empty: return None (no memory tool)
```

The fallback preserves backward compatibility for developers running the runtime
locally without mounted secrets.

---

## Design Decisions

| Decision | Rationale |
|---|---|
| File mounts not `envFrom` | Smaller exfiltration surface; ESO's natural output; decoupled from pod lifecycle |
| One volume per memory tool, not one shared volume | Tool name is in the mount path; avoids cross-tool secret collision; each Secret has its own lifecycle |
| Secret name `kape-tool-<name>-conn` | `kape-tool-` prefix scopes to KAPE; `-conn` distinguishes connection secrets from future credential types |
| Volume name `kape-tool-<name>-secrets` | Matches Secret name pattern; `-secrets` is consistent with existing `settings` volume naming |
| `KAPE_SECRETS_DIR` configurable | Allows unit tests to use a temp dir; useful for non-standard cluster layouts |
| `KAPE_TOOL_NAME` injected by operator | Runtime needs tool name to construct the path; operator already knows it at Deployment build time |
| File-first, env var fallback | Local development works without secret infra; production always uses files |
| Only memory-type KapeTool secrets moved | LLM API key (handler's `llm.provider` credential) is a separate concern not in scope |
| ESO manifests are reference examples, not operator-generated | Follows the same pattern as NetworkPolicy and RBAC reference manifests (spec 0007 §2, §3); operator does not own external secret infrastructure |
| Vault as ESO backend in examples | Most complete ESO backend example; teams using AWS SM / GCP SM can adapt the `provider` stanza |

---

## Work Items

### Part A — ESO example manifests

**Files to create:**

- `examples/eso/secretstore.yaml`
- `examples/eso/externalsecret.yaml`
- `examples/eso/README.md`

**`examples/eso/secretstore.yaml`** — ESO `SecretStore` using Vault as backend:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: kape-vault-backend
  namespace: kape-system
spec:
  provider:
    vault:
      server: "https://vault.example.com"
      path: "secret"
      version: "v2"
      auth:
        kubernetes:
          mountPath: "kubernetes"
          role: "kape-system"
```

**`examples/eso/externalsecret.yaml`** — `ExternalSecret` for a memory tool:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: kape-tool-order-memory-conn
  namespace: kape-system
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: kape-vault-backend
    kind: SecretStore
  target:
    name: kape-tool-order-memory-conn
  data:
    - secretKey: qdrant_url
      remoteRef:
        key: kape/tools/order-memory
        property: qdrant_url
    - secretKey: qdrant_collection
      remoteRef:
        key: kape/tools/order-memory
        property: qdrant_collection
```

The `target.name` (`kape-tool-order-memory-conn`) must match the Secret name the
operator expects for the tool named `order-memory`.

**`examples/eso/README.md`** must explain:

- Prerequisites: ESO CRDs installed, `SecretStore` authenticated to backend
- How to adapt the Vault `SecretStore` to AWS Secrets Manager or GCP Secret Manager
- The `target.name` naming convention (`kape-tool-<name>-conn`) and why it must match
- The two expected Secret keys (`qdrant_url`, `qdrant_collection`) and their values
- How to verify: `kubectl get secret kape-tool-order-memory-conn -o yaml`

---

### Part B — Operator file mounts (`operator/infra/k8s/deployment.go`)

**Current state:**

`buildDeployment()` iterates `tools []v1alpha1.KapeTool` only to detect MCP-type
tools (sets `hasMCPTools`). Memory-type tools currently inject `QDRANT_URL` and
`QDRANT_COLLECTION` as env vars elsewhere in the reconciliation path (or expect
them to be set by the engineer).

**Changes required in `buildDeployment()`:**

1. Iterate `tools` to collect memory-type tools:

```go
var memoryTools []v1alpha1.KapeTool
for _, t := range tools {
    if t.Spec.Type == "memory" {
        memoryTools = append(memoryTools, t)
    }
}
```

2. For each memory tool, append a `Volume` to `volumes`:

```go
for _, mt := range memoryTools {
    secretName := "kape-tool-" + mt.Name + "-conn"
    volName    := "kape-tool-" + mt.Name + "-secrets"
    volumes = append(volumes, corev1.Volume{
        Name: volName,
        VolumeSource: corev1.VolumeSource{
            Secret: &corev1.SecretVolumeSource{
                SecretName: secretName,
            },
        },
    })
}
```

3. For each memory tool, append a `VolumeMount` to `handlerVolumeMounts`:

```go
for _, mt := range memoryTools {
    volName   := "kape-tool-" + mt.Name + "-secrets"
    mountPath := "/etc/kape/secrets/" + mt.Name
    handlerVolumeMounts = append(handlerVolumeMounts, corev1.VolumeMount{
        Name:      volName,
        MountPath: mountPath,
        ReadOnly:  true,
    })
}
```

4. For each memory tool, append `KAPE_TOOL_NAME` env var to the handler container
   so the runtime knows which path prefix to use:

```go
for _, mt := range memoryTools {
    envVars = append(envVars, corev1.EnvVar{
        Name:  "KAPE_TOOL_NAME",
        Value: mt.Name,
    })
}
```

   Note: if multiple memory tools are attached to a single handler in the future,
   this pattern must be revisited. For v1, one memory tool per handler is the
   expected configuration. If more than one memory tool is detected, the operator
   should emit a warning condition to `KapeHandler.status` and use the first tool's
   name, to avoid ambiguity.

5. **Remove** any existing logic that injects `QDRANT_URL` or `QDRANT_COLLECTION`
   as plain env vars from tool secrets.

**No changes required in:**

- `buildKapeproxySidecar()` — the sidecar does not need access to Qdrant secrets
- `DeploymentAdapter.Ensure()` — the patch logic is unchanged
- Any other file in `operator/infra/k8s/`

---

### Part C — Runtime file reads (`runtime/src/kape_runtime/memory.py`)

**Current state:**

`build_memory_tool()` reads `QDRANT_URL` and `QDRANT_COLLECTION` directly from
`os.environ`. Returns `None` if either is absent.

**New `build_memory_tool()` implementation:**

```python
def build_memory_tool(
    *,
    vector_store_factory: Callable[..., Any] = _default_vector_store_factory,
    embedding_factory: Callable[[], Any] = _default_embedding_factory,
) -> BaseTool | None:
    """Build a `search_memory` tool backed by a Qdrant vector store.

    Read order for connection details:
      1. File at $KAPE_SECRETS_DIR/<tool-name>/qdrant_url  (file mount from Secret)
      2. Environment variable QDRANT_URL                   (local dev fallback)

    Returns None if either value cannot be resolved — handlers without a memory
    backend simply omit the tool from the graph.
    """
    secrets_dir = os.environ.get("KAPE_SECRETS_DIR", "/etc/kape/secrets")
    tool_name   = os.environ.get("KAPE_TOOL_NAME")

    def _read_secret(filename: str, env_fallback: str) -> str | None:
        if tool_name:
            path = os.path.join(secrets_dir, tool_name, filename)
            if os.path.exists(path):
                return pathlib.Path(path).read_text().strip()
        return os.environ.get(env_fallback)

    url        = _read_secret("qdrant_url", "QDRANT_URL")
    collection = _read_secret("qdrant_collection", "QDRANT_COLLECTION")

    if not url or not collection:
        return None

    embedding = embedding_factory()
    vstore = vector_store_factory(
        url=url,
        collection_name=collection,
        embedding=embedding,
    )
    retriever = vstore.as_retriever(search_kwargs={"k": 5})

    def _search(query: str) -> str:
        docs = retriever.invoke(query)
        return "\n\n".join(d.page_content for d in docs)

    return Tool(
        name="search_memory",
        description=(
            "Search persistent memory for prior incidents and notes related to the "
            "current event. Pass a search query string; returns matching documents "
            "concatenated as plain text."
        ),
        func=_search,
    )
```

**Import addition required:** `import pathlib` at the top of the file (alongside
existing `import os`).

The `_read_secret` helper is a module-private function. It is not part of the public
API of `memory.py`.

---

## Key Files

| File | Change type |
|---|---|
| `examples/eso/secretstore.yaml` | New |
| `examples/eso/externalsecret.yaml` | New |
| `examples/eso/README.md` | New |
| `operator/infra/k8s/deployment.go` | Modified — `buildDeployment()` adds Volume + VolumeMount + KAPE_TOOL_NAME per memory tool; removes QDRANT_* env var injection |
| `runtime/src/kape_runtime/memory.py` | Modified — `build_memory_tool()` reads from file with env var fallback; adds `import pathlib` |

---

## Acceptance Criteria

1. `kubectl apply -f examples/eso/` succeeds on a cluster with ESO CRDs installed
   (both `SecretStore` and `ExternalSecret` are accepted without validation errors).

2. When a `KapeHandler` reconciles with a memory-type `KapeTool` named `order-memory`,
   the resulting Deployment spec contains:
   - A `volumes` entry with `name: kape-tool-order-memory-secrets` backed by
     `secret.secretName: kape-tool-order-memory-conn`
   - A `volumeMounts` entry on the `handler` container with
     `mountPath: /etc/kape/secrets/order-memory` and `readOnly: true`
   - An env var `KAPE_TOOL_NAME=order-memory` on the `handler` container
   - No env var named `QDRANT_URL` or `QDRANT_COLLECTION` injected by the operator

3. `build_memory_tool()` returns a working tool when:
   - `KAPE_SECRETS_DIR` points to a temp directory
   - `KAPE_TOOL_NAME=test-tool` is set
   - Files `<tmpdir>/test-tool/qdrant_url` and `<tmpdir>/test-tool/qdrant_collection`
     exist with valid values
   - No `QDRANT_URL` or `QDRANT_COLLECTION` env vars are set

4. `build_memory_tool()` returns a working tool (env var fallback path) when:
   - `KAPE_TOOL_NAME` is not set
   - `QDRANT_URL` and `QDRANT_COLLECTION` env vars are set
   - No secret files exist

5. `build_memory_tool()` returns `None` when neither files nor env vars provide
   both values.

---

## Testing Strategy

### Part A (ESO manifests)

- Dry-run acceptance: `kubectl apply --dry-run=server -f examples/eso/` against a
  cluster with ESO installed. No code test required.
- Documentation review: README.md covers the four items listed in the Work Items
  section above.

### Part B (Operator — deployment.go)

Extend the existing deployment builder unit tests in
`operator/infra/k8s/deployment_test.go`:

- **`TestBuildDeployment_MemoryToolVolume`**: Pass a `KapeTool` with `Spec.Type = "memory"`
  and `Name = "order-memory"`. Assert:
  - `dep.Spec.Template.Spec.Volumes` contains a volume named
    `kape-tool-order-memory-secrets` with `SecretName = "kape-tool-order-memory-conn"`
  - `handler` container `VolumeMounts` contains a mount at
    `/etc/kape/secrets/order-memory` with `ReadOnly = true`
  - `handler` container `Env` contains `KAPE_TOOL_NAME=order-memory`
  - `handler` container `Env` does NOT contain `QDRANT_URL` or `QDRANT_COLLECTION`

- **`TestBuildDeployment_NoMemoryTool`**: Pass only MCP-type tools. Assert that no
  `kape-tool-*-secrets` volume or mount appears.

### Part C (Runtime — memory.py)

Add to `runtime/tests/test_memory.py` (new file if it does not exist):

- **`test_reads_from_file`**: Create a temp directory tree with
  `<tmpdir>/my-tool/qdrant_url` and `<tmpdir>/my-tool/qdrant_collection`. Set
  `KAPE_SECRETS_DIR=<tmpdir>` and `KAPE_TOOL_NAME=my-tool`. Assert that
  `build_memory_tool()` (with injected factories) is not `None` and that
  `vector_store_factory` was called with the file content as `url` and
  `collection_name`.

- **`test_env_var_fallback`**: Unset `KAPE_TOOL_NAME`. Set `QDRANT_URL` and
  `QDRANT_COLLECTION`. Assert that `build_memory_tool()` calls `vector_store_factory`
  with the env var values.

- **`test_returns_none_when_no_values`**: Unset all four inputs (file + env). Assert
  `build_memory_tool()` returns `None`.

- **`test_file_takes_precedence_over_env`**: Create secret files with value `file-url`.
  Set `QDRANT_URL=env-url`. Assert `vector_store_factory` is called with `url="file-url"`.

Run tests with:
```
conda run -n kape-runtime pytest runtime/tests/test_memory.py -v
```
