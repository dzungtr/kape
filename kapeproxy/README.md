# kapeproxy

`kapeproxy` is the per-handler MCP proxy sidecar for KAPE. One instance runs alongside each `KapeHandler` pod, fronting all MCP-typed `KapeTool` upstreams behind a single `:8080` MCP endpoint. The handler runtime talks to `kapeproxy` instead of to N individual sidecars.

## Responsibilities

- **Namespaced tool fan-out.** Exposes upstream tools as `{kapetool-name}__{tool-name}`. The handler runtime sees one logical MCP server; kapeproxy routes each call to the right upstream.
- **Allowlist enforcement.** Honours `KapeTool.spec.mcp.allowedTools`. A call to a disallowed tool returns MCP error `-32601` and never reaches the upstream.
- **Field-level redaction.** Applies `KapeTool.spec.mcp.redaction.input` to arguments before calling the upstream and `redaction.output` to the response before returning it to the runtime.
- **Audit logging.** One structured zerolog entry per call (`tool.namespaced_name`, `tool.upstream`, `tool.original_name`, `tool.allowed`, `tool.latency_ms`, `error`, `kape.task_id`).
- **OTEL tracing.** One `kapeproxy.tool_call` span per call with the same attributes as the audit entry, plus W3C TraceContext extracted from inbound HTTP headers and propagated to outbound MCP calls. Exporter is OTLP HTTP, configured via `OTEL_EXPORTER_OTLP_ENDPOINT`.

## Configuration

`kapeproxy` reads its configuration from `/etc/kapeproxy/config.yaml` (override with the `KAPEPROXY_CONFIG_PATH` env var). The YAML schema is rendered by the operator from `KapeTool.spec.mcp` — see `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md` §2.2 for the full schema.

Example:

```yaml
upstreams:
  grafana-mcp:
    url: http://grafana-mcp:8080
    transport: streamable-http
    allowedTools:
      - query_dashboards
      - get_alert
    redaction:
      input:
        - jsonPath: "$.token"
      output:
        - jsonPath: "$.data.email"
    audit: true
```

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `KAPEPROXY_CONFIG_PATH` | `/etc/kapeproxy/config.yaml` | Config file location |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none — OTEL SDK default) | OTLP HTTP endpoint (e.g. `http://otel-collector:4318`) |

## Graceful shutdown

`kapeproxy` listens for SIGTERM and SIGINT. On signal it stops accepting new requests, drains in-flight requests for up to 30 seconds, closes upstream connections, flushes OTEL spans, and exits.

## Container image

Built from `Dockerfile` (multi-stage; final base is distroless static). The operator pulls the image referenced by `kape-config`'s `kapeproxy.image` and `kapeproxy.version` keys; see Phase 6 spec §2 for those defaults.

## Development

```bash
go test ./...
go build ./cmd/kapeproxy
```

The integration test at `kapeproxy/integration_test.go` spins up an in-process mock MCP server and exercises the full request path including allowlist enforcement, redaction, and unreachable-upstream behaviour. No external dependencies required.
