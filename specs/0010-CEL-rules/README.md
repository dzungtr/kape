# KAPE — CRD CEL Validation Rules

**Status:** Draft
**Author:** Dzung Tran
**Session:** 10 — CRD CEL Validation Rules
**Created:** 2026-04-03
**Depends on:** `kape-crd-rfc.md` (rev 5), `kape-security-design.md`

---

## Table of Contents

1. [Overview](#1-overview)
2. [Enforcement Architecture](#2-enforcement-architecture)
3. [KapeHandler — ValidatingWebhookConfiguration](#3-kapehandler--validatingwebhookconfiguration)
4. [KapeTool — x-kubernetes-validations](#4-kapetool--x-kubernetes-validations)
5. [KapeSchema — x-kubernetes-validations](#5-kapeschema--x-kubernetes-validations)
6. [Webhook Implementation](#6-webhook-implementation)
7. [Test Cases](#7-test-cases)
8. [Decision Record](#8-decision-record)

---

## 1. Overview

KAPE CRD validation is split across two mechanisms with a clean boundary:

- **`KapeHandler`** — a validating webhook inside the operator binary. Used exclusively because H1 requires a cross-resource existence check (`schemaRef` must reference a live `KapeSchema`), which standard CEL cannot perform.
- **`KapeTool`** and **`KapeSchema`** — standard `x-kubernetes-validations` CEL rules embedded directly in the CRD OpenAPI schema. No webhook involved.

This split keeps the webhook surface minimal — exactly one CRD type — and everything else is evaluated natively by the Kubernetes API server with no operator involvement.

**Rule inventory:**

| CRD           | Mechanism                      | Rule count |
| ------------- | ------------------------------ | ---------- |
| `KapeHandler` | ValidatingWebhookConfiguration | 3          |
| `KapeTool`    | `x-kubernetes-validations`     | 3          |
| `KapeSchema`  | `x-kubernetes-validations`     | 4          |

**Out of scope for admission validation.** The following are surfaced as reconciler `status.conditions` after apply, not rejected at admission time:

| Constraint                                                         | Condition type            | Reason                                                      |
| ------------------------------------------------------------------ | ------------------------- | ----------------------------------------------------------- |
| `spec.llm.maxIterations > 100`                                     | `LLMConfigWarning`        | Soft limit — handler still runs                             |
| `spec.scaling.maxReplicas > 50`                                    | `ScalingConfigWarning`    | Soft limit — handler still runs                             |
| `spec.llm.systemPrompt` missing `<context>` or `UNTRUSTED`         | `PromptInjectionWarning`  | Freetext check is too brittle for hard reject               |
| `spec.scaling.scaleToZero: true` + `spec.scaling.minReplicas >= 1` | `ScalingConfigured=False` | Contradictory but not malformed — operator surfaces clearly |

---

## 2. Enforcement Architecture

```
Engineer applies KapeHandler
       │
       ▼
ValidatingWebhookConfiguration
  kapehandler.kape.io (failurePolicy: Fail)
       │
       ├── H1: schemaRef non-empty + KapeSchema exists in namespace
       ├── H2: minReplicas <= maxReplicas
       └── H3: llm.provider is supported value
       │
       ▼ (if all pass)
Resource persisted to etcd
       │
       ▼
KapeHandlerReconciler picks up
  → evaluates soft constraints
  → writes status.conditions for warnings

Engineer applies KapeTool / KapeSchema
       │
       ▼
API server evaluates x-kubernetes-validations CEL inline
  (no webhook, no operator involvement)
       │
       ├── KapeTool:  T1, T2, T3
       └── KapeSchema: S1, S2, S3, S4
       │
       ▼ (if all pass)
Resource persisted to etcd
```

**`failurePolicy: Fail`** — if the operator webhook is unreachable, `KapeHandler` applies are rejected. A misconfigured handler that bypasses validation because the webhook was down is worse than a temporary apply failure. A documented break-glass procedure (patch `failurePolicy` to `Ignore` during operator upgrades) is provided in the operator runbook.

---

## 3. KapeHandler — ValidatingWebhookConfiguration

### 3.1 Webhook Registration

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: kape-handler-validator
  annotations:
    cert-manager.io/inject-ca-from: kape-system/kape-operator-webhook-cert
spec:
  webhooks:
    - name: kapehandler.kape.io
      admissionReviewVersions: ["v1"]
      sideEffects: None
      failurePolicy: Fail
      matchPolicy: Equivalent
      rules:
        - apiGroups: ["kape.io"]
          apiVersions: ["v1alpha1"]
          operations: ["CREATE", "UPDATE"]
          resources: ["kapehandlers"]
          scope: "Namespaced"
      clientConfig:
        service:
          name: kape-operator-webhook
          namespace: kape-system
          port: 9443
          path: /validate-kape-io-v1alpha1-kapehandler
        caBundle: "" # injected by cert-manager
      namespaceSelector:
        matchExpressions:
          - key: kubernetes.io/metadata.name
            operator: NotIn
            values: ["kube-system"]
```

The webhook TLS certificate is issued by cert-manager from the `kape-system` Issuer. The `caBundle` field is populated automatically via the `cert-manager.io/inject-ca-from` annotation. No manual certificate management.

### 3.2 H1 — `schemaRef` Non-Empty and KapeSchema Existence

**Purpose:** prevents a `KapeHandler` from being applied with a dangling `schemaRef`. A missing schema causes a silent runtime failure — the handler pod starts, attempts to build its Pydantic model, finds no schema, and crashes. Rejecting at admission gives the engineer an immediate, actionable error.

Cross-resource existence checks cannot be expressed in standard CEL `x-kubernetes-validations`. The webhook uses the operator's existing informer cache — no additional Kubernetes API calls at admission time.

```go
// Pseudocode — implemented in infra/webhook/handler.go
func (w *KapeHandlerWebhook) validateSchemaRef(
    ctx context.Context,
    handler *v1alpha1.KapeHandler,
) *field.Error {
    if strings.TrimSpace(handler.Spec.SchemaRef) == "" {
        return field.Required(
            field.NewPath("spec", "schemaRef"),
            "schemaRef must not be empty",
        )
    }

    exists := w.informerCache.KapeSchemaExists(
        handler.Namespace,
        handler.Spec.SchemaRef,
    )
    if !exists {
        return field.Invalid(
            field.NewPath("spec", "schemaRef"),
            handler.Spec.SchemaRef,
            fmt.Sprintf(
                "KapeSchema %q not found in namespace %q",
                handler.Spec.SchemaRef,
                handler.Namespace,
            ),
        )
    }

    return nil
}
```

**Rejection message seen by the engineer:**

```
Error from server (KapeSchema "karpenter-decision-schema" not found in namespace "kape-system"):
  error when applying patch: admission webhook "kapehandler.kape.io" denied the request:
  spec.schemaRef: Invalid value: "karpenter-decision-schema":
  KapeSchema "karpenter-decision-schema" not found in namespace "kape-system"
```

**Note on deletion order:** because `KapeSchemaReconciler` uses a finalizer (`kape.io/schema-protection`) to block `KapeSchema` deletion while any `KapeHandler` references it, the schema will always be present for the duration of any handler's lifetime. H1 covers the creation/update path; the finalizer covers the deletion path.

### 3.3 H2 — `minReplicas` ≤ `maxReplicas`

**Purpose:** prevents a contradictory scaling configuration that would cause KEDA to behave unpredictably. Both fields have defaults (`min=1`, `max=10`) but an engineer who sets only `maxReplicas: 0` produces an invalid state.

```go
func (w *KapeHandlerWebhook) validateScaling(
    handler *v1alpha1.KapeHandler,
) *field.Error {
    scaling := handler.Spec.Scaling
    if scaling == nil {
        return nil // defaults applied by operator — no validation needed
    }

    if scaling.MinReplicas > scaling.MaxReplicas {
        return field.Invalid(
            field.NewPath("spec", "scaling", "minReplicas"),
            scaling.MinReplicas,
            fmt.Sprintf(
                "minReplicas (%d) must be less than or equal to maxReplicas (%d)",
                scaling.MinReplicas,
                scaling.MaxReplicas,
            ),
        )
    }

    return nil
}
```

**Rejection message seen by the engineer:**

```
Error from server: admission webhook "kapehandler.kape.io" denied the request:
  spec.scaling.minReplicas: Invalid value: 5:
  minReplicas (5) must be less than or equal to maxReplicas (3)
```

### 3.4 H3 — `llm.provider` Supported Value

**Purpose:** prevents a handler from being applied with an unsupported LLM provider. An unsupported value causes the handler pod to crash at startup when attempting to initialise the LangChain client. Rejecting at admission surfaces the error immediately with the full list of valid values.

```go
var supportedLLMProviders = []string{
    "anthropic",
    "openai",
    "azure-openai",
    "ollama",
}

func (w *KapeHandlerWebhook) validateLLMProvider(
    handler *v1alpha1.KapeHandler,
) *field.Error {
    provider := handler.Spec.LLM.Provider

    for _, supported := range supportedLLMProviders {
        if provider == supported {
            return nil
        }
    }

    return field.NotSupported(
        field.NewPath("spec", "llm", "provider"),
        provider,
        supportedLLMProviders,
    )
}
```

**Rejection message seen by the engineer:**

```
Error from server: admission webhook "kapehandler.kape.io" denied the request:
  spec.llm.provider: Unsupported value: "bedrock":
  supported values: "anthropic", "openai", "azure-openai", "ollama"
```

**Adding a new provider** requires updating `supportedLLMProviders` in the operator and redeploying. This is intentional — a new provider requires handler runtime support before the operator should accept it.

### 3.5 Webhook Handler Wiring

All three rules are evaluated in a single webhook handler. All errors are collected before returning — the engineer sees all failures in one apply, not one at a time.

```go
// infra/webhook/handler.go
func (w *KapeHandlerWebhook) Handle(
    ctx context.Context,
    req admission.Request,
) admission.Response {
    handler := &v1alpha1.KapeHandler{}
    if err := w.decoder.Decode(req, handler); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }

    var allErrs field.ErrorList

    if err := w.validateSchemaRef(ctx, handler); err != nil {
        allErrs = append(allErrs, err)
    }
    if err := w.validateScaling(handler); err != nil {
        allErrs = append(allErrs, err)
    }
    if err := w.validateLLMProvider(handler); err != nil {
        allErrs = append(allErrs, err)
    }

    if len(allErrs) > 0 {
        return admission.Denied(allErrs.ToAggregate().Error())
    }

    return admission.Allowed("")
}
```

---

## 4. KapeTool — x-kubernetes-validations

All three rules are embedded in the `KapeTool` CRD OpenAPI schema under `x-kubernetes-validations`. They are evaluated by the Kubernetes API server on every CREATE and UPDATE with no operator involvement.

### 4.1 T1 — `memory.backend` Supported Value

**Purpose:** enforces that the operator knows how to provision the requested vector DB backend. An unsupported value would cause the `KapeToolReconciler` to enter a terminal error state with no actionable message. Admission-time rejection is cleaner.

**Applies only when `spec.type == "memory"`** — the rule is a no-op for `mcp` and `event-publish` types.

```yaml
# Embedded in KapeTool CRD spec.versions[].schema.openAPIV3Schema
x-kubernetes-validations:
  - rule: |
      self.spec.type != "memory" ||
        self.spec.memory.backend in ["qdrant", "pgvector", "weaviate"]
    message: "spec.memory.backend must be one of: qdrant, pgvector, weaviate"
    fieldPath: .spec.memory.backend
```

**Rejection message seen by the engineer:**

```
The KapeTool "karpenter-memory" is invalid:
  spec.memory.backend: Invalid value: "pinecone":
  spec.memory.backend must be one of: qdrant, pgvector, weaviate
```

### 4.2 T2 — `memory.distanceMetric` Supported Value

**Purpose:** enforces that the distance metric is one the Qdrant/pgvector/Weaviate provisioner knows how to configure. An unsupported value would cause silent provisioning failure or incorrect retrieval behaviour.

**Applies only when `spec.type == "memory"`.**

```yaml
- rule: |
    self.spec.type != "memory" ||
      self.spec.memory.distanceMetric in ["cosine", "dot", "euclidean"]
  message: "spec.memory.distanceMetric must be one of: cosine, dot, euclidean"
  fieldPath: .spec.memory.distanceMetric
```

**Rejection message seen by the engineer:**

```
The KapeTool "karpenter-memory" is invalid:
  spec.memory.distanceMetric: Invalid value: "manhattan":
  spec.memory.distanceMetric must be one of: cosine, dot, euclidean
```

### 4.3 T3 — `eventPublish.type` Subject Prefix

**Purpose:** enforces that the CloudEvents `type` field on event-publish tools follows the `kape.events.` convention. A subject outside this prefix would be silently ignored by the NATS stream (which filters on `kape.events.>`), causing downstream handlers to never receive the event with no error surfaced.

**Applies only when `spec.type == "event-publish"`.**

```yaml
- rule: |
    self.spec.type != "event-publish" ||
      self.spec.eventPublish.type.startsWith("kape.events.")
  message: "spec.eventPublish.type must start with 'kape.events.'"
  fieldPath: .spec.eventPublish.type
```

**Rejection message seen by the engineer:**

```
The KapeTool "notify-slack-platform" is invalid:
  spec.eventPublish.type: Invalid value: "notifications.slack":
  spec.eventPublish.type must start with 'kape.events.'
```

### 4.4 Complete KapeTool CRD Validation Block

These three rules sit together under the CRD schema. Shown here in context for the implementer embedding them:

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: kapetools.kape.io
spec:
  group: kape.io
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: |
                self.spec.type != "memory" ||
                  self.spec.memory.backend in ["qdrant", "pgvector", "weaviate"]
              message: "spec.memory.backend must be one of: qdrant, pgvector, weaviate"
              fieldPath: .spec.memory.backend

            - rule: |
                self.spec.type != "memory" ||
                  self.spec.memory.distanceMetric in ["cosine", "dot", "euclidean"]
              message: "spec.memory.distanceMetric must be one of: cosine, dot, euclidean"
              fieldPath: .spec.memory.distanceMetric

            - rule: |
                self.spec.type != "event-publish" ||
                  self.spec.eventPublish.type.startsWith("kape.events.")
              message: "spec.eventPublish.type must start with 'kape.events.'"
              fieldPath: .spec.eventPublish.type
          # ... remainder of schema
```

---

## 5. KapeSchema — x-kubernetes-validations

All four rules are embedded in the `KapeSchema` CRD OpenAPI schema. They enforce the secure schema design requirements established in `kape-security-design.md` Layer 5 — specifically the structural properties that prevent prompt injection via schema output fields.

### 5.1 S1 — `version` Format

**Purpose:** enforces predictable version strings. `v1`, `v2`, `v10` are valid. `1.0`, `latest`, `beta` are not. Consistent version naming is required for the schema versioning model (new name on breaking change) to work reliably — engineers must be able to sort and compare versions by inspection.

```yaml
x-kubernetes-validations:
  - rule: self.spec.version.matches("^v[0-9]+$")
    message: "spec.version must match pattern v[0-9]+ (e.g. v1, v2, v10)"
    fieldPath: .spec.version
```

**Rejection message seen by the engineer:**

```
The KapeSchema "karpenter-decision-schema" is invalid:
  spec.version: Invalid value: "1.0":
  spec.version must match pattern v[0-9]+ (e.g. v1, v2, v10)
```

### 5.2 S2 — `required` Array Non-Empty

**Purpose:** prevents a schema that structurally validates any object — a `required: []` schema imposes no constraints on LLM output, making `KapeSchema` meaningless and defeating the `validate_schema` guard node. Every schema must enforce at least one field.

```yaml
- rule: size(self.spec.jsonSchema.required) > 0
  message: "spec.jsonSchema.required must contain at least one field name"
  fieldPath: .spec.jsonSchema.required
```

**Rejection message seen by the engineer:**

```
The KapeSchema "karpenter-decision-schema" is invalid:
  spec.jsonSchema.required: Invalid value: []:
  spec.jsonSchema.required must contain at least one field name
```

### 5.3 S3 — `additionalProperties` Must Be `false`

**Purpose:** prevents extra fields being smuggled into the decision object stored in the `schema_output` JSONB audit column. A schema without `additionalProperties: false` allows the LLM to emit arbitrary additional fields that bypass the declared structure — a potential exfiltration channel and an audit integrity concern. This is a hard security requirement per `kape-security-design.md` Layer 5.

```yaml
- rule: |
    has(self.spec.jsonSchema.additionalProperties) &&
      self.spec.jsonSchema.additionalProperties == false
  message: "spec.jsonSchema.additionalProperties must be set to false"
  fieldPath: .spec.jsonSchema.additionalProperties
```

**Rejection message seen by the engineer:**

```
The KapeSchema "karpenter-decision-schema" is invalid:
  spec.jsonSchema.additionalProperties: Invalid value: true:
  spec.jsonSchema.additionalProperties must be set to false
```

### 5.4 S4 — All `required` Fields Exist in `properties`

**Purpose:** prevents a schema where a required field is declared in `required[]` but has no definition in `properties`. This would cause the Pydantic model generation at handler pod startup to fail with a cryptic error rather than giving the engineer an actionable message at apply time.

```yaml
- rule: |
    self.spec.jsonSchema.required.all(f,
      f in self.spec.jsonSchema.properties
    )
  message: >
    all fields listed in spec.jsonSchema.required must be
    defined in spec.jsonSchema.properties
  fieldPath: .spec.jsonSchema.required
```

**Rejection message seen by the engineer:**

```
The KapeSchema "karpenter-decision-schema" is invalid:
  spec.jsonSchema.required: Invalid value: ["decision", "confidence", "rootcause"]:
  all fields listed in spec.jsonSchema.required must be
  defined in spec.jsonSchema.properties
```

(In this example, `rootcause` appears in `required` but has no entry in `properties`.)

### 5.5 Complete KapeSchema CRD Validation Block

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: kapeschemas.kape.io
spec:
  group: kape.io
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-validations:
            - rule: self.spec.version.matches("^v[0-9]+$")
              message: "spec.version must match pattern v[0-9]+ (e.g. v1, v2, v10)"
              fieldPath: .spec.version

            - rule: size(self.spec.jsonSchema.required) > 0
              message: "spec.jsonSchema.required must contain at least one field name"
              fieldPath: .spec.jsonSchema.required

            - rule: |
                has(self.spec.jsonSchema.additionalProperties) &&
                  self.spec.jsonSchema.additionalProperties == false
              message: "spec.jsonSchema.additionalProperties must be set to false"
              fieldPath: .spec.jsonSchema.additionalProperties

            - rule: |
                self.spec.jsonSchema.required.all(f,
                  f in self.spec.jsonSchema.properties
                )
              message: >
                all fields listed in spec.jsonSchema.required must be
                defined in spec.jsonSchema.properties
              fieldPath: .spec.jsonSchema.required
          # ... remainder of schema
```

---

## 6. Webhook Implementation

### 6.1 Operator Integration

The webhook handler is implemented inside the operator binary and registered with the controller-runtime manager. It uses the operator's existing informer cache for H1's existence check — no additional API server calls.

```go
// cmd/main.go — webhook registration alongside reconcilers
func main() {
    // ... manager setup, reconciler wiring ...

    // Webhook
    webhookHandler := &webhook.KapeHandlerWebhook{
        InformerCache: mgr.GetCache(),
        Decoder:       admission.NewDecoder(mgr.GetScheme()),
    }

    mgr.GetWebhookServer().Register(
        "/validate-kape-io-v1alpha1-kapehandler",
        &admission.Webhook{Handler: webhookHandler},
    )

    mgr.Start(ctrl.SetupSignalHandler())
}
```

### 6.2 Webhook TLS

TLS for the webhook endpoint is managed by cert-manager. The operator webhook server uses the certificate mounted at `/tmp/k8s-webhook-server/serving-certs/` — the controller-runtime default path.

```yaml
# Certificate for webhook TLS — issued by kape-system Issuer
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-operator-webhook-cert
  namespace: kape-system
spec:
  secretName: kape-operator-webhook-cert
  dnsNames:
    - kape-operator-webhook.kape-system.svc
    - kape-operator-webhook.kape-system.svc.cluster.local
  issuerRef:
    name: kape-ca
    kind: ClusterIssuer
```

```yaml
# Service exposing the operator's webhook port
apiVersion: v1
kind: Service
metadata:
  name: kape-operator-webhook
  namespace: kape-system
spec:
  selector:
    app: kape-operator
  ports:
    - name: webhook
      port: 9443
      targetPort: 9443
```

### 6.3 Operator Deployment — Webhook Port

The operator Deployment spec (from `kape-operator-design.md`) gains one additional container port and volume mount:

```yaml
containers:
  - name: kape-operator
    ports:
      - name: webhook
        containerPort: 9443
        protocol: TCP
    volumeMounts:
      - name: webhook-cert
        mountPath: /tmp/k8s-webhook-server/serving-certs
        readOnly: true
volumes:
  - name: webhook-cert
    secret:
      secretName: kape-operator-webhook-cert
```

---

## 7. Test Cases

Each rule has a set of valid and invalid inputs. These serve as the acceptance criteria for the implementation and as regression tests in the operator's integration test suite.

### 7.1 H1 — schemaRef Existence

| Input                                                  | Expected   | Notes                   |
| ------------------------------------------------------ | ---------- | ----------------------- |
| `schemaRef: karpenter-decision-schema` (schema exists) | ✅ Allowed | Normal case             |
| `schemaRef: karpenter-decision-schema` (schema absent) | ❌ Denied  | H1 cross-resource check |
| `schemaRef: ""`                                        | ❌ Denied  | Empty string            |
| `schemaRef` field absent                               | ❌ Denied  | Missing required field  |

### 7.2 H2 — minReplicas ≤ maxReplicas

| Input                  | Expected   | Notes                        |
| ---------------------- | ---------- | ---------------------------- |
| `min: 1, max: 5`       | ✅ Allowed | Normal case                  |
| `min: 0, max: 0`       | ✅ Allowed | Scale-to-zero valid          |
| `min: 1, max: 1`       | ✅ Allowed | Fixed single replica         |
| `min: 5, max: 3`       | ❌ Denied  | min > max                    |
| `min: 1, max: 0`       | ❌ Denied  | min > max                    |
| `scaling` field absent | ✅ Allowed | Defaults applied by operator |

### 7.3 H3 — llm.provider Supported Value

| Input                    | Expected   | Notes          |
| ------------------------ | ---------- | -------------- |
| `provider: anthropic`    | ✅ Allowed |                |
| `provider: openai`       | ✅ Allowed |                |
| `provider: azure-openai` | ✅ Allowed |                |
| `provider: ollama`       | ✅ Allowed |                |
| `provider: bedrock`      | ❌ Denied  | Unsupported    |
| `provider: gemini`       | ❌ Denied  | Unsupported    |
| `provider: ""`           | ❌ Denied  | Empty          |
| `provider: Anthropic`    | ❌ Denied  | Case-sensitive |

### 7.4 T1 — memory.backend Supported Value

| Input                             | Expected   | Notes                              |
| --------------------------------- | ---------- | ---------------------------------- |
| `type: memory, backend: qdrant`   | ✅ Allowed |                                    |
| `type: memory, backend: pgvector` | ✅ Allowed |                                    |
| `type: memory, backend: weaviate` | ✅ Allowed |                                    |
| `type: memory, backend: pinecone` | ❌ Denied  | Unsupported                        |
| `type: memory, backend: chroma`   | ❌ Denied  | Unsupported                        |
| `type: mcp` (no memory field)     | ✅ Allowed | Rule is no-op for non-memory types |

### 7.5 T2 — memory.distanceMetric Supported Value

| Input                                     | Expected   | Notes         |
| ----------------------------------------- | ---------- | ------------- |
| `type: memory, distanceMetric: cosine`    | ✅ Allowed |               |
| `type: memory, distanceMetric: dot`       | ✅ Allowed |               |
| `type: memory, distanceMetric: euclidean` | ✅ Allowed |               |
| `type: memory, distanceMetric: manhattan` | ❌ Denied  | Unsupported   |
| `type: mcp` (no memory field)             | ✅ Allowed | Rule is no-op |

### 7.6 T3 — eventPublish.type Subject Prefix

| Input                                                                     | Expected   | Notes                |
| ------------------------------------------------------------------------- | ---------- | -------------------- |
| `type: event-publish, eventPublish.type: kape.events.notifications.slack` | ✅ Allowed |                      |
| `type: event-publish, eventPublish.type: kape.events.gitops.pr-requested` | ✅ Allowed |                      |
| `type: event-publish, eventPublish.type: notifications.slack`             | ❌ Denied  | Missing prefix       |
| `type: event-publish, eventPublish.type: events.kape.slack`               | ❌ Denied  | Wrong prefix order   |
| `type: event-publish, eventPublish.type: kape.event.slack`                | ❌ Denied  | `event` not `events` |
| `type: mcp` (no eventPublish field)                                       | ✅ Allowed | Rule is no-op        |

### 7.7 S1 — version Format

| Input             | Expected   | Notes               |
| ----------------- | ---------- | ------------------- |
| `version: v1`     | ✅ Allowed |                     |
| `version: v2`     | ✅ Allowed |                     |
| `version: v10`    | ✅ Allowed | Multi-digit version |
| `version: 1.0`    | ❌ Denied  | Missing `v` prefix  |
| `version: v1.0`   | ❌ Denied  | Dot not permitted   |
| `version: latest` | ❌ Denied  | Non-numeric         |
| `version: v`      | ❌ Denied  | No digit after `v`  |
| `version: ""`     | ❌ Denied  | Empty               |

### 7.8 S2 — required Non-Empty

| Input                              | Expected   | Notes                                        |
| ---------------------------------- | ---------- | -------------------------------------------- |
| `required: [decision, confidence]` | ✅ Allowed |                                              |
| `required: [decision]`             | ✅ Allowed | Single field                                 |
| `required: []`                     | ❌ Denied  | Empty array                                  |
| `required` field absent            | ❌ Denied  | Required by OpenAPI schema `required` clause |

### 7.9 S3 — additionalProperties False

| Input                         | Expected   | Notes                  |
| ----------------------------- | ---------- | ---------------------- |
| `additionalProperties: false` | ✅ Allowed |                        |
| `additionalProperties: true`  | ❌ Denied  |                        |
| `additionalProperties` absent | ❌ Denied  | Must be explicitly set |

### 7.10 S4 — required Fields Exist in properties

| Input                                                                                                  | Expected   | Notes                               |
| ------------------------------------------------------------------------------------------------------ | ---------- | ----------------------------------- |
| `required: [decision], properties: {decision: {...}}`                                                  | ✅ Allowed |                                     |
| `required: [decision, confidence], properties: {decision: {...}, confidence: {...}, reasoning: {...}}` | ✅ Allowed | Extra properties are fine           |
| `required: [decision, rootcause], properties: {decision: {...}}`                                       | ❌ Denied  | `rootcause` missing from properties |
| `required: [decision], properties: {}`                                                                 | ❌ Denied  | `decision` missing from properties  |

---

## 8. Decision Record

| Decision                                                                                                | Rationale                                                                                                                                                                          | Session |
| ------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| `KapeHandler` validation via webhook only — no `x-kubernetes-validations`                               | H1 requires cross-resource existence check; impossible in standard CEL. One admission path per CRD — simpler to test and reason about.                                             | 7       |
| `KapeTool` and `KapeSchema` validation via `x-kubernetes-validations` only — no webhook                 | Webhook scope kept minimal. All KapeTool and KapeSchema rules validate against closed value sets KAPE itself defines — no cross-resource checks needed.                            | 7       |
| `failurePolicy: Fail` on webhook                                                                        | A handler that bypasses validation due to webhook downtime is worse than a temporary apply failure. Break-glass procedure documented in operator runbook.                          | 7       |
| Webhook uses informer cache for H1 — no API calls at admission time                                     | Avoids admission latency and API server load under concurrent applies. Cache is warm — operator already watches KapeSchema resources.                                              | 10      |
| All webhook errors collected before returning                                                           | Engineer sees all failures in one apply. `field.ErrorList` with `ToAggregate()` surfaces all violations together.                                                                  | 10      |
| T1, T2, T3 rules are no-ops for non-matching `spec.type`                                                | CEL conditional on `spec.type` prevents false positives when fields for a different type are absent.                                                                               | 10      |
| S3 (`additionalProperties: false`) is a hard reject                                                     | Security requirement from Layer 5: prevents LLM output smuggling extra fields into the `schema_output` JSONB audit column.                                                         | 7       |
| No CEL rule on `trigger.type` subject prefix                                                            | Left as documented convention — not a hard constraint. An engineer publishing to a non-`kape.events.*` subject is misconfiguring their handler, not violating a security boundary. | 10      |
| No CEL rule on `mcp.upstream.url` internal-only constraint                                              | Engineer is trusted to configure MCP correctly. NetworkPolicy (Layer 0) is the operational control, not admission.                                                                 | 7       |
| No CEL rule on bare `*` in `allowedTools`                                                               | Left as documented best practice. The sidecar pre-registration filter is the enforcement point.                                                                                    | 10      |
| Soft constraints (maxIterations, maxReplicas, prompt injection warning) as reconciler status conditions | These are advisory — handlers still run. Hard reject on freetext or soft numeric limits would be brittle and hostile to legitimate use cases.                                      | 7       |
