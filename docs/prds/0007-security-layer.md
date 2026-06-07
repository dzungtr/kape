# KAPE Security Layer

## Status
**Draft**

## Problem Statement
KAPE processes untrusted event data through LLM agents that can call Kubernetes MCP servers, creating a broad attack surface spanning prompt injection, unauthorised tool calls, PII leakage, lateral pod movement, and audit-tampering. Without a structured, multi-layer security model, a single compromised layer (e.g., a prompt injection that bypasses the LLM) could escalate to uncontrolled cluster modification. A defence-in-depth architecture is needed so that compromise of any single layer does not cascade into a cluster-wide breach.

## Goals
- Enforce network isolation between handler pods, MCP servers, NATS, and the audit database so that lateral movement from any compromised pod is blocked at the network layer.
- Prevent the LLM from ever seeing or calling tools that are not explicitly allowed by the engineer, via sidecar pre-registration filtering at startup.
- Redact sensitive fields (secrets, PII, free-text annotations) from MCP tool inputs and outputs before they reach the LLM or the audit log.
- Detect and surface missing prompt-injection mitigations (context isolation, untrusted-data instruction) in KapeHandler system prompts.
- Validate LLM output structure against a user-defined schema and halt execution on non-conforming output before any action runs.

## Non-Goals
- Enforcing mTLS between the kapetool sidecar and the upstream MCP server (deferred until the MCP authentication specification matures).
- Automatic generation of NetworkPolicies by the KAPE operator (reference manifests only; engineers apply them manually).
- Database-level immutability enforcement (row-level security, immutability triggers) on the audit log (architectural isolation via single-accessor pattern is the v1 security property; DB hardening is deferred).

## User Stories
- As a platform engineer, I want to deploy KAPE with reference NetworkPolicy and RBAC manifests so that I can enforce network boundaries and least-privilege access without reverse-engineering the component topology.
- As a platform engineer, I want to configure an allowedTools list on my KapeTool so that the LLM can never call a tool I have not explicitly approved.
- As a platform engineer, I want to specify jsonPath redaction rules on my KapeTool so that sensitive fields (secrets, tokens, annotations) are stripped from MCP responses before reaching the LLM or the audit log.
- As a platform engineer, I want to define a KapeSchema with enum-constrained decision fields so that LLM output is structurally validated and injected values cannot set arbitrary decision outcomes.
- As a platform engineer, I want my KapeHandler to be rejected at apply time if spec.llm.provider is unsupported or spec.schemaRef references a nonexistent KapeSchema, so that misconfigurations are caught before the handler starts.

## Functional Requirements
- FR-1: The kapetool sidecar MUST fetch the full tool catalogue from its upstream MCP server at startup, filter it against the allowedTools list (exact match and fnmatch glob), and expose only the filtered catalogue via its own tools/list endpoint.
- FR-2: The kapetool sidecar MUST verify at call time that the requested tool name exists in the filtered catalogue before forwarding to the upstream MCP server (defence-in-depth against race conditions).
- FR-3: The kapetool sidecar MUST apply jsonPath-based input and output redaction rules (configured via KapeTool.spec.mcp.redaction) to tool arguments and responses, replacing matched fields with the string "[REDACTED]".
- FR-4: The handler runtime MUST register a PIIRedactionCallback (a BaseCallbackHandler subclass) on the LLM instance that applies default regex patterns (email, IPv4, AWS access key, Bearer token, password) to all LLM inputs and outputs.
- FR-5: The operator MUST check KapeHandler.spec.llm.systemPrompt for the presence of "<context>" or "UNTRUSTED" and write a PromptInjectionWarning status condition if neither is found.
- FR-6: The validating webhook for KapeHandler MUST reject applies where spec.schemaRef is empty or references a KapeSchema that does not exist in the same namespace.
- FR-7: The validating webhook for KapeHandler MUST reject applies where spec.scaling.minReplicas is greater than spec.scaling.maxReplicas.
- FR-8: The validating webhook for KapeHandler MUST reject applies where spec.llm.provider is not one of "anthropic", "openai", "azure-openai", or "ollama".
- FR-9: KapeSchema CRD MUST enforce, via x-kubernetes-validations CEL rules, that spec.jsonSchema has additionalProperties set to false, a non-empty required array, and that all required fields are defined in properties.
- FR-10: The validate_schema LangGraph node MUST write Task{status: SchemaValidationFailed} and halt execution when LLM output fails Pydantic validation against the KapeSchema.
- FR-11: Only kape-task-service MUST hold PostgreSQL credentials and have a network path to the database; handler pods, the operator, and the dashboard MUST access the database exclusively through the kape-task-service REST API.
- FR-12: kape_schema_validation_failures_total Prometheus counter MUST be incremented on each schema validation failure.

## Technical Context
The security design spans seven independent layers that together form a defence-in-depth posture for KAPE's agent runtime. Each layer is independently implementable and auditable, ensuring that compromise of any single layer does not cascade into uncontrolled cluster modification.

Layer 0 (Network Isolation) ships reference NetworkPolicy and CiliumNetworkPolicy manifests in two CNI dialects (standard and Cilium). These enforce five network boundaries: handler pod egress (only NATS on 4222, kape-task-service on 8080, and LLM APIs on 443), kapetool sidecar egress (only its upstream MCP server), MCP server ingress (only handler pods in kape-system), kape-task-service ingress (handler pods and dashboard), and PostgreSQL ingress (only kape-task-service). The operator does not generate NetworkPolicies automatically; engineers apply reference manifests as part of cluster setup. Labels from the kape.io/component, kape.io/tool, and kape.io/mcp-server conventions drive podSelector rules.

Layer 1 (MCP Server RBAC) provides reference ServiceAccount/ClusterRole/ClusterRoleBinding and RoleBinding manifests for the two v1 reference MCP servers (k8s-mcp-read and k8s-mcp-write). The key architectural constraint is that write-tool ClusterRoles are bound exclusively via namespace-scoped RoleBindings, never via ClusterRoleBinding. A recommended Kyverno ClusterPolicy (block-kape-mcp-write-clusterrolebinding) ships as an optional guard. This layer relates to ADR decisions about how agent permissions are scoped.

Layers 2 through 5 operate inside the handler pod at runtime. The kapetool sidecar (Layer 2) enforces tool allowlisting via pre-registration filtering at startup, with call-time verification as defence-in-depth. Input/output redaction (Layer 3) operates at two levels: sidecar jsonPath-based field redaction (configured per KapeTool) and a LangChain-level PIIRedactionCallback on the ChatAnthropic instance. Prompt injection defence (Layer 4) uses three vectors: XML <context> isolation with HTML escaping for event data, sidecar output redaction on injection-prone fields, and KapeSchema enum constraints on decision fields. The operator surfaces a PromptInjectionWarning status condition when the system prompt lacks "<context>" or "UNTRUSTED". KapeSchema validation (Layer 5) uses a Pydantic model generated from the CRD's jsonSchema field at handler pod startup, enforced by the validate_schema LangGraph node.

Admission validation (Layer 6) is split by mechanism: KapeHandler uses a three-rule ValidatingWebhookConfiguration (schemaRef existence, scaling bounds, supported LLM provider) implemented in the operator binary; KapeTool and KapeSchema use standard x-kubernetes-validations CEL rules embedded in their CRD manifests, avoiding webhook complexity where cross-resource checks are not required.

Layer 7 (Immutable Audit Log) relies on architectural isolation: only kape-task-service connects to PostgreSQL. Database-level hardening (role separation, RLS, immutability triggers) is explicitly deferred until compliance requirements justify the added complexity. Retention is implemented via pg_partman monthly partitioning, configurable at Helm install time (postgres.retentionDays).

The design relates to ADR 0001 (label taxonomy) through the kape.io/component, kape.io/tool, and kape.io/mcp-server labels used in NetworkPolicy selectors. The PromptInjectionWarning condition and SchemaValidationFailed status follow the pattern of handler status conditions that would be documented in the KapeHandler CRD spec.

## Design Tenets
- Each layer must be independently implementable and independently auditable; no layer should rely on another layer for its core controls.
- Reference manifests (NetworkPolicy, RBAC) must ship in the Helm chart rather than being generated by the operator, keeping operator scope tight and giving engineers control over their deployment manifests.
- Admission validation must use the simplest mechanism that meets the requirement: webhooks only where cross-resource checks are needed (schemaRef existence); x-kubernetes-validations CEL for all field-level constraints within a single CRD.

## Open Questions
- Should the PIIRedactionCallback default patterns be configurable via KapeConfig in v1 rather than deferred to v2, given that email/IP patterns vary significantly across deployment environments?
- Is the failurePolicy: Fail default for the KapeHandler webhook acceptable for production rollouts, or should the break-glass procedure to override to Ignore be documented before v1 GA?
- Should the rate limiting on the kape-task-service REST API (identified as a v1 patch release gap) be included in the v1 scope to protect against handler-pod bugs flooding the database?

## Future Considerations
- mTLS authentication between the kapetool sidecar and the upstream MCP server, to be revisited when the MCP authentication specification is widely adopted.
- Operator-generated NetworkPolicies for Boundaries 1 and 2 (handler pod egress and kapetool sidecar egress), removing the need for engineers to maintain reference manifests.
- Database-level immutability enforcement (immutability triggers on tasks, row-level security on tool_audit_log, separate read-only database roles) when audit integrity becomes a formal compliance requirement.
- Extensible PII patterns via KapeConfig.spec.piiPatterns, allowing engineers to add custom regex patterns without subclassing PIIRedactionCallback.

## References
- [Security Design Spec](docs/specs/0007-security-layer/README.md) — the original spec this PRD derives from
