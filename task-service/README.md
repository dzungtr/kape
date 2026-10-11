# Task Service

> ⚠️ **Deprecated** — this component is part of the v1 KAPE platform, superseded by the greenfield successor (#150), whose controller owns the CRUD job. Deprecated does **not** mean deleted: the component stays in place as a re-role candidate for the successor platform.

The task tracking microservice. It persists one record per Handler invocation — event, reasoning trace, decision, and actions — and surfaces it as the audit unit of the v1 platform. See the repo root `README.md` and `CONTEXT.md` for the platform model.
