# Title: CRD CEL Validation Rules for KAPE Custom Resources

## Status
**Draft**

## Problem Statement
KAPE custom resources (`KapeHandler`, `KapeTool`, and `KapeSchema`) currently lack admission-time validation, which allows invalid configurations to be persisted to etcd. These invalid resources cause silent runtime failures -- handler pods that crash at startup due to dangling schema references, unsupported LLM providers, or contradictory scaling configurations -- that are harder to diagnose than a rejected apply. Without structured validation, engineers receive cryptic error messages from operator reconcilers rather than immediate, actionable feedback at apply time.

## Goals
- Reject invalid `KapeHandler` resources at admission time with actionable error messages for three classes of misconfiguration: dangling schema references, unsupported LLM providers, and contradictory scaling fields.
- Enforce supported value sets and structural constraints on `KapeTool` and `KapeSchema` resources using standard Kubernetes CEL (`x-kubernetes-validations`) without a webhook.
- Surface soft constraints (numeric limits, prompt injection warnings) as reconciler `status.conditions` rather than hard rejections at admission.
- Provide clear rejection messages that tell the engineer exactly which field is invalid and what the valid options are.
- Include a documented break-glass procedure for patching the webhook `failurePolicy` to `Ignore` during operator upgrades.

## Non-Goals
- Not to replace or duplicate validation already performed by the operator reconciler at runtime -- admission validation catches structural and config errors only.
- Not to validate the content of free-text fields such as `spec.llm.systemPrompt` or `spec.mcp.upstream.url`, which are governed by documented convention and network policy rather than admission-level enforcement.
- Not to introduce a new database or external dependency for validation -- the webhook uses the existing operator informer cache.

## User Stories
- As an engineer applying a KapeHandler, I want a dangling schemaRef to be rejected at apply time with the name of the missing KapeSchema, so that I know immediately that the referenced schema does not exist rather than discovering it when the handler pod crashes.
- As an engineer applying a KapeTool with a memory backend, I want unsupported values for `memory.backend` and `memory.distanceMetric` to be rejected at apply time, so that I do not silently provision a vector DB backend the operator cannot manage.
- As an engineer applying a KapeSchema, I want structural errors (missing required fields, incorrect `additionalProperties`, malformed version strings) to be rejected at apply time, so that Pydantic model generation at handler startup does not fail with a cryptic error.
- As a platform operator, I want the webhook to fail closed when the operator is unreachable, so that misconfigured handlers that bypass validation are not applied without review.
- As a KAPE developer adding a new LLM provider, I want to extend the list of supported providers in a single operator code location and redeploy, so that the validation rule is automatically updated.

## Functional Requirements
- FR-1: On every CREATE and UPDATE of a `KapeHandler`, the validating webhook must check that `spec.schemaRef` is non-empty and that a `KapeSchema` with that name exists in the same namespace, using the operator's informer cache.
- FR-2: On every CREATE and UPDATE of a `KapeHandler`, the validating webhook must reject the resource if `spec.scaling.minReplicas` exceeds `spec.scaling.maxReplicas`, or return an appropriate default-based pass if the scaling block is absent.
- FR-3: On every CREATE and UPDATE of a `KapeHandler`, the validating webhook must reject the resource if `spec.llm.provider` is not one of `anthropic`, `openai`, `azure-openai`, or `ollama`, with the rejection message listing all supported values.
- FR-4: The webhook handler must collect all validation errors before returning, so that the engineer sees all rule violations in a single `kubectl apply` response.
- FR-5: On every CREATE and UPDATE of a `KapeTool`, the API server must reject the resource via inline CEL if `spec.type == "memory"` and `spec.memory.backend` is not one of `qdrant`, `pgvector`, `weaviate`.
- FR-6: On every CREATE and UPDATE of a `KapeTool`, the API server must reject the resource via inline CEL if `spec.type == "memory"` and `spec.memory.distanceMetric` is not one of `cosine`, `dot`, `euclidean`.
- FR-7: On every CREATE and UPDATE of a `KapeTool`, the API server must reject the resource via inline CEL if `spec.type == "event-publish"` and `spec.eventPublish.type` does not start with `kape.events.`.
- FR-8: On every CREATE and UPDATE of a `KapeSchema`, the API server must reject the resource via inline CEL if `spec.version` does not match the pattern `^v[0-9]+$`.
- FR-9: On every CREATE and UPDATE of a `KapeSchema`, the API server must reject the resource via inline CEL if `spec.jsonSchema.additionalProperties` is not explicitly set to `false`.
- FR-10: On every CREATE and UPDATE of a `KapeSchema`, the API server must reject the resource via inline CEL if `spec.jsonSchema.required` is empty.
- FR-11: On every CREATE and UPDATE of a `KapeSchema`, the API server must reject the resource via inline CEL if any entry in `spec.jsonSchema.required` does not have a corresponding definition in `spec.jsonSchema.properties`.
- FR-12: The webhook must be registered with `failurePolicy: Fail` so that if the operator is unreachable, `KapeHandler` applies are rejected.
- FR-13: The webhook TLS certificate must be managed by cert-manager via the `cert-manager.io/inject-ca-from` annotation with no manual certificate management.
- FR-14: Soft constraints (`maxIterations > 100`, `maxReplicas > 50`, suspicious `systemPrompt` content, contradictory `scaleToZero` and `minReplicas`) must not be rejected at admission and must instead be surfaced as reconciler `status.conditions`.

## Technical Context

This feature introduces admission validation for the three KAPE custom resource types defined in the operator's CRD set. The validation is split across two enforcement mechanisms with a clean architectural boundary: a validating webhook for `KapeHandler` (because rule H1 requires a cross-resource existence check that standard CEL cannot perform), and inline `x-kubernetes-validations` CEL rules embedded in the CRD OpenAPI schema for `KapeTool` and `KapeSchema` (all rules validate against closed value sets KAPE defines itself, requiring no cross-resource checks).

The `KapeHandler` webhook lives inside the operator binary and is registered with the controller-runtime manager. For the H1 schema-existence check, it uses the operator's existing informer cache (already watching `KapeSchema` resources) rather than making additional API server calls at admission time, which avoids admission latency and API server load under concurrent applies. All three webhook rules (H1, H2, H3) are evaluated in a single handler, with errors collected into a `field.ErrorList` so that the engineer sees all violations in one response rather than one at a time. The webhook is registered with `failurePolicy: Fail` -- a handler that bypasses validation because the webhook was down is worse than a temporary apply failure. A documented break-glass procedure (patching `failurePolicy` to `Ignore` during operator upgrades) is provided in the operator runbook.

The KapeSchema deletion-protection finalizer (`kape.io/schema-protection`) documented in ADR 0032 ("Block KapeSchema deletion while referenced via a deletion-protection finalizer") covers the deletion path complementing H1's creation/update path. The webhook TLS certificate is managed by cert-manager from a `ClusterIssuer` using ECDSA P-256 keys with TLS 1.3 minimum, consistent with the cert-manager architecture established in ADR 0060 and ADR 0061. The in-process webhook server pattern follows the operator's existing controller-runtime infrastructure.

Soft constraints (numeric limits, prompt injection warnings, contradictory scaling intent) are intentionally deferred to the reconciler layer as `status.conditions` rather than rejected at admission. This aligns with ADR 0075 ("Resolution and gating on aggregate roots") -- the reconciler is the authority on operational warnings, not the admission webhook.

## Design Tenets
- The CEL rules for `KapeTool` must be no-ops when `spec.type` does not match the relevant type condition, preventing false positives when fields for a different tool type are absent.
- The webhook must not introduce any new databases, external services, or Kubernetes API calls at admission time -- the informer cache alone must satisfy the H1 existence check.
- All `KapeSchema` structural constraints (version format, `additionalProperties: false`, required field integrity) are hard security requirements originating from the security design described in `docs/specs/0010-CEL-rules/README.md` Section 5 and `kape-security-design.md` Layer 5, not optional style preferences.

## Open Questions
- What is the exact break-glass procedure for patching `failurePolicy` to `Ignore` during operator upgrades, and where should it be documented (operator runbook, upgrade guide, or both)?
- Should the webhook handler emit structured audit events for denied requests, or is the Kubernetes API server's native admission audit logging sufficient?
- Are there any existing `KapeHandler` or `KapeTool` deployments in development environments that would be rejected by the new validation rules after upgrade, and should a migration pre-flight check be provided?

## Future Considerations
- Adding validation for `spec.trigger.type` subject prefix via CEL (currently a documented convention only -- see Decision Record in the spec).
- Adding validation for `spec.mcp.upstream.url` to reject internal-only addresses (currently governed by NetworkPolicy rather than admission).
- Adding validation for bare `*` in `spec.allowedTools` (currently a documented best practice with enforcement at the sidecar pre-registration filter).
- Extending the supported LLM provider list over time as new providers gain handler runtime support.

## References
- Original spec: `docs/specs/0010-CEL-rules/README.md`
- ADR 0032: `docs/adr/0032-kapeschema-deletion-protection-finalizer.md` (deletion-protection finalizer for KapeSchema)
- ADR 0060: `docs/adr/0060-cert-manager-self-signed-clusterissuer-hierarchy.md` (cert-manager architecture)
- ADR 0061: `docs/adr/0061-ecdsa-p256-keys-tls-1-3-minimum.md` (TLS key and protocol standards)
- ADR 0075: `docs/adr/0075-resolution-and-gating-on-aggregate-roots.md` (reconciler as authority on operational warnings)
