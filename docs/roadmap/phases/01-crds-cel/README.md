# Phase 1 — CRDs + CEL Validation

**Status:** done
**Milestone:** —
**Specs:** 0002, 0010
**Modified by:** 0012 (created), 0013 (KapeSkill CRD types + spec.skills[] field added)

## Goal

Establish the API contract that every other component reads or writes. Nothing else can be built correctly without the CRD types being final.

## Reference Specs

- `0002-crds-design` — all CRD field schemas, types, and validation annotations
- `0010-CEL-rules` — complete CEL rules to embed as `x-kubernetes-validations` and the `ValidatingWebhookConfiguration` for cross-field rules
- `0013-kape-skill-crd` — KapeSkill CRD types, KapeHandler spec.skills[] field

## Work

- Write Go struct types for `KapeHandler`, `KapeTool`, `KapeSchema`, `KapeSkill` in `operator/infra/api/v1alpha1/`
- Annotate with controller-gen markers (`+kubebuilder:validation:*`, `+kubebuilder:object:root=true`)
- Add `spec.skills[]` field to KapeHandler type (list of skill refs)
- Run `make generate` → emit CRD YAML into `crds/`
- Embed all CEL validation rules from spec 0010 as `x-kubernetes-validations` in the CRD YAML
- Apply CRDs to a local kind cluster
- Write a `ValidatingWebhookConfiguration` for cross-field KapeHandler rules that cannot be expressed in CEL alone (per spec 0010)

## Acceptance Criteria

- `kubectl apply -f crds/` succeeds on a fresh kind cluster
- `kubectl apply` of a KapeHandler with `confidenceThreshold: 0.5` is rejected with a clear CEL message
- `kubectl apply` of a KapeTool with both `allow` and `deny` set is rejected
- `kubectl apply` of a valid KapeHandler, KapeTool, KapeSchema, KapeSkill all succeed

## Key Files

- `operator/infra/api/v1alpha1/kapehandler_types.go`
- `operator/infra/api/v1alpha1/kapetool_types.go`
- `operator/infra/api/v1alpha1/kapeschema_types.go`
- `operator/infra/api/v1alpha1/kapeskill_types.go`
- `crds/` (generated)
