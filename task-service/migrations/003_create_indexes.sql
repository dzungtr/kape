-- +migrate Up
CREATE INDEX idx_tasks_received_at
    ON tasks (received_at DESC);

CREATE INDEX idx_tasks_handler
    ON tasks (handler, received_at DESC);

CREATE INDEX idx_tasks_status
    ON tasks (status, received_at DESC);

CREATE INDEX idx_tasks_retry_of
    ON tasks (retry_of)
    WHERE retry_of IS NOT NULL;

-- +migrate Down
DROP INDEX IF EXISTS idx_tasks_retry_of;
DROP INDEX IF EXISTS idx_tasks_status;
DROP INDEX IF EXISTS idx_tasks_handler;
DROP INDEX IF EXISTS idx_tasks_received_at;
