-- +migrate Up
CREATE TABLE tasks (
    id              TEXT        PRIMARY KEY,
    cluster         TEXT        NOT NULL,
    handler         TEXT        NOT NULL,
    namespace       TEXT        NOT NULL,
    event_id        TEXT        NOT NULL,
    event_source    TEXT        NOT NULL,
    event_type      TEXT        NOT NULL,
    event_raw       JSONB       NOT NULL,
    status          task_status NOT NULL,
    dry_run         BOOLEAN     NOT NULL DEFAULT false,
    schema_output   JSONB,
    actions         JSONB,
    error           JSONB,
    retry_of        TEXT        REFERENCES tasks(id),
    otel_trace_id   TEXT,
    received_at     TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ,
    duration_ms     INTEGER
) PARTITION BY RANGE (received_at);

-- Pre-create partitions for current and next month (adjust years as needed)
CREATE TABLE tasks_2026_04 PARTITION OF tasks
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');

CREATE TABLE tasks_2026_05 PARTITION OF tasks
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

-- +migrate Down
DROP TABLE IF EXISTS tasks_2026_05;
DROP TABLE IF EXISTS tasks_2026_04;
DROP TABLE IF EXISTS tasks;
