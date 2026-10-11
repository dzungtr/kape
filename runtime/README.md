# KAPE Runtime

> ⚠️ **Deprecated** — this component is part of the v1 KAPE platform, superseded by the greenfield successor (#150), whose controller owns the CRUD job. Deprecated does **not** mean deleted: the component stays in place as a re-role candidate for the successor platform.

The Python + LangGraph agent runtime. It runs inside a Handler pod, consumes an event from the broker, and executes the ReAct loop defined by the materialised `KapeHandler` configuration. See the repo root `README.md` and `CONTEXT.md` for the platform model.
