# KAPE Operator

## Status
**Draft**

## Problem Statement
KAPE currently relies on manual infrastructure provisioning for each handler runtime deployment. There is no automated lifecycle management for handler pods, vector databases (Qdrant), MCP tool endpoints, schema validation, or skill content distribution. As the platform scales beyond a handful of handlers, manual per-component setup becomes error-prone and unsustainable — the operator is the single control plane component that materialises all handler dependencies from declarative CRD specs.

## Goals
- Automatically provision and manage Qdrant StatefulSets for memory-type tools from `KapeTool` CRDs.
- Render and maintain handler `settings.toml` ConfigMaps, `kapeproxy-config` ConfigMaps, and lazy skill file ConfigMaps from `KapeHandler` and `KapeSkill` specs.
- Inject a single `kapeproxy` sidecar per handler pod, replacing per-tool `kapetool` sidecars.
- Validate all handler dependencies (schemas, tools, skills) before deploying, with clear status conditions and Kubernetes Events for failures.
- Generate KEDA ScaledObjects for NATS-based autoscaling, and protect `KapeSchema` and `KapeSkill` resources from deletion while referenced by active handlers.

## Non-Goals
- Managing MCP server lifecycle or NATS JetStream configuration — these remain external concerns configured by the platform engineer.
- Creating Kubernetes RBAC for handler ServiceAccounts — handler pods have zero Kubernetes API permissions, and cloud workload identity annotations are reserved for a future iteration.
- Reading or writing runtime telemetry — telemetry is the handler runtime's responsibility; the operator only emits its own Kubernetes Events and Prometheus metrics.

## User Stories
- As a platform engineer, I want to define a `KapeHandler` CRD with a schema reference, tool references, and skill references so that the operator automatically provisions all infrastructure and configuration needed for that handler.
- As a platform engineer, I want the operator to validate all dependencies (schema, tools, skills) before deploying a handler pod, so that I receive clear error signals if a referenced resource is missing or not ready.
- As a platform engineer, I want to define `KapeSkill` resources with eager or lazy loading mode so that I can control which instructions are embedded in the system prompt versus loaded on-demand at runtime.
- As a platform engineer, I want `KapeSchema` and `KapeSkill` resources to be protected from accidental deletion while referenced by active handlers, so that I do not inadvertently break running handler deployments.
- As a platform engineer, I want the operator to compute a rollout hash over handler spec plus all dependency specs so that changes to schemas, tools, or skills automatically trigger handler pod rollouts.

## Functional Requirements
- FR-1: `KapeToolReconciler` must provision a Qdrant StatefulSet, Service, and PVC for each `KapeTool` of `type: memory`, and perform periodic health probes for `type: mcp` tools.
- FR-2: `KapeSchemaReconciler` must validate schema content, manage the `kape.io/schema-protection` finalizer to block deletion while handlers reference the schema, and signal handler rollouts on `status.schemaHash` changes.
- FR-3: `KapeSkillReconciler` must validate that `spec.instruction` and `spec.description` are non-empty, verify that all referenced tools exist and are Ready, manage the `kape.io/skill-protection` finalizer, and re-enqueue every 30 seconds for periodic tool readiness refresh.
- FR-4: `KapeHandlerReconciler` must check all dependencies — schema, tools, and skills — before proceeding to deployment, setting `DependenciesReady=False` with the appropriate reason and message when any dependency is missing or not ready.
- FR-5: `KapeHandlerReconciler` must compute a unified tool map from the handler's own tool references and all referenced skills' tool references, deduplicating by `KapeTool` name, and use this map for kapeproxy-config rendering, rollout hash computation, and label sync.
- FR-6: `KapeHandlerReconciler` must assemble the system prompt by appending eager skill instructions (in declaration order) and a lazy skill preamble listing available skills with their descriptions, with lazy loading triggered at runtime via `load_skill`.
- FR-7: `KapeHandlerReconciler` must render a `kapeproxy-config` ConfigMap from the unified tool map containing upstream URL, transport, allowed tools, redaction config, and audit flag for each MCP-type tool.
- FR-8: `KapeHandlerReconciler` must create a `kape-skills-{handler-name}` ConfigMap with one `.txt` entry per lazy-loaded skill, and mount it at `/etc/kape/skills` on the handler container, only if at least one lazy skill exists.
- FR-9: `KapeHandlerReconciler` must inject exactly one `kapeproxy` sidecar container per handler pod (no per-tool `kapetool` sidecars), with the kapeproxy-config ConfigMap mounted at `/etc/kapeproxy`.
- FR-10: The operator must create a dedicated ServiceAccount per `KapeHandler` with zero Kubernetes RBAC and `automountServiceAccountToken: false` on the pod spec.
- FR-11: The operator must create a KEDA `ScaledObject` per `KapeHandler` for NATS-based autoscaling, and detect consumer name changes to recreate the ScaledObject when needed.
- FR-12: The operator must emit Prometheus metrics for all four CRD types, including a `kape_skills_total` gauge counted by namespace and readiness state.

## Technical Context
The KAPE Operator is a Kubernetes controller built with `controller-runtime`, deployed as a single Deployment in the `kape-system` namespace. It manages four CRD types — `KapeHandler`, `KapeTool`, `KapeSchema`, and `KapeSkill` — via four dedicated reconcilers. The operator is the sole component responsible for infrastructure provisioning; the handler runtime never reads Kubernetes CRDs directly. All configuration is materialised into mounted ConfigMaps (`settings.toml`, `kapeproxy-config`, `kape-skills`) before handler pods start.

The operator architecture uses cross-resource watches via label-based map functions. When a `KapeTool`, `KapeSchema`, or `KapeSkill` changes, the affected `KapeHandler` reconcilers are re-enqueued. A rollout hash (`kape.io/rollout-hash`) is computed as a sha256 of the handler spec plus all dependency specs (schema, tools, skills), and annotated on the pod template — changes trigger Deployment rollouts automatically. Deletion protection is enforced via finalizers: `kape.io/schema-protection` on `KapeSchema` and `kape.io/skill-protection` on `KapeSkill`.

This design builds on ADR 0001 (Github Label Taxonomy) for tagging project issues and the broader architecture decisions in `docs/adr/`. The operator replaces prior ad-hoc provisioning patterns with declarative CRD-driven lifecycle management.

## Design Tenets
- The handler runtime must never call the Kubernetes API — the operator fully materialises all config into files and env vars before pods start.
- Handler ServiceAccounts carry zero Kubernetes RBAC; cloud workload identity annotations are reserved for a future iteration and must not be backfilled without explicit ADR coverage.
- No per-tool `kapetool` sidecars — a single `kapeproxy` sidecar per pod proxies all MCP tool calls, centralising OTEL, tool filtering, and namespace prefixing.

## Open Questions
- What is the exact mechanism for MCP server discovery and registration with kapeproxy? The spec describes upstream URLs from `KapeTool` specs, but the initial tool list bootstrap (tools/list handshake) warrants end-to-end testing in a real cluster before finalising.
- Should the operator support an admission webhook for synchronous CRD validation, or is controller-side validation on reconcile sufficient for v1?
- What is the disaster recovery procedure if the operator Deployment is lost and needs to be recreated — are all child resources recoverable solely from CRD manifests?

## Future Considerations
- Cloud workload identity annotations (IRSA for EKS, GKE Workload Identity) on handler ServiceAccounts are reserved for a future iteration.
- Qdrant StatefulSet PVCs are currently orphaned on `KapeTool` deletion (PV retained) — a PVC reclamation policy may be added later.
- The operator currently runs as a single Deployment in `kape-system`; multi-tenancy (namespace-scoped operator instances per tenant) is deferred.

## References
- [KAPE Operator — Technical Design](../docs/specs/0005-kape-operator/README.md)
