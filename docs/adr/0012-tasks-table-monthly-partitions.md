# Partition the tasks table monthly by received_at

## Status

accepted

## Context

The tasks table grows continuously with every event and is queried predominantly by recent time ranges, so a single unpartitioned table would become unwieldy for retention and query performance. A partitioning strategy plus partition-management mechanism was needed.

## Decision

Partition `tasks` by `RANGE (received_at)` into monthly partitions, with `EnsurePartition` called at startup and by a monthly CronJob to pre-create the next partition, and four targeted indexes for the common access patterns.

## Consequences

Time-range queries and retention operate per-partition for better performance, but partition creation must be kept ahead of incoming data via the CronJob, and missing a future partition would cause insert failures.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
