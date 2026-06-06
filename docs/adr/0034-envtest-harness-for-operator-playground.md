# Use an envtest harness instead of a real cluster for the operator playground

## Status

accepted

## Context

Exercising the operator + CRD reconciliation flow locally normally requires a real Kubernetes cluster or cloud services, which is heavy and slow for interactive developer verification. The playground needs operators to reconcile real Kubernetes resources without that overhead.

## Decision

Run the operator against a controller-runtime envtest harness (apiserver + etcd binaries fetched via `setup-envtest`) in a separate Go binary at `playground/operator/main.go` that installs CRDs, runs the reconciler, and writes a `kubeconfig.playground` to the repo root.

## Consequences

Developers can apply KapeHandler YAML and watch real reconciliation via `kubectl --kubeconfig kubeconfig.playground` without a cluster. The operator runs as a separate process outside compose (blocking on Ctrl-C), and cluster-dependent behavior like KEDA ScaledObject acting is not exercised since envtest has no controllers acting on created resources.

## Source

- [2026-05-03-local-dev-playground-design.md](../../superpowers/specs/2026-05-03-local-dev-playground-design.md)
