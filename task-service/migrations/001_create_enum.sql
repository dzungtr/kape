-- +migrate Up
CREATE TYPE task_status AS ENUM (
    'Processing',
    'Completed',
    'Failed',
    'SchemaValidationFailed',
    'ActionError',
    'UnprocessableEvent',
    'PendingApproval',
    'Timeout',
    'Retried'
);

-- +migrate Down
DROP TYPE IF EXISTS task_status;
