# Do not ship a cluster-wide default-deny policy

## Status

accepted

## Context

The reference manifests are additive allowances that only have their intended effect on top of a deny-all posture; shipping a cluster-wide default-deny as part of this deliverable could conflict with engineers' existing cluster baselines.

## Decision

Omit a default-deny NetworkPolicy from the deliverable and require engineers to apply one independently as a cluster baseline before the reference allowances take effect.

## Consequences

Engineers retain control over their cluster-wide deny posture and avoid conflicts with existing baselines. The risk is that without a pre-existing deny-all, the additive allow rules provide no actual isolation, so correct deployment depends on an undocumented external prerequisite.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
