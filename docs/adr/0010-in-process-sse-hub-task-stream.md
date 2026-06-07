# Stream task events via an in-process SSE hub

## Status

accepted

## Context

The dashboard needs near-real-time delivery of new and updated task events without polling, and the service must fan out a single task event to many connected dashboard clients while tolerating slow consumers. A streaming mechanism and back-pressure policy were required.

## Decision

Expose `GET /tasks/stream` as Server-Sent Events backed by an in-process `sse.Hub` that implements the domain `Stream` port, using an RWMutex-guarded subscriber map of buffered channels and dropping messages to subscribers whose channel is full.

## Consequences

Clients receive live updates over plain HTTP with simple fan-out, but the drop-on-full policy means slow clients silently miss events, and the in-process hub does not fan out across multiple service replicas.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
