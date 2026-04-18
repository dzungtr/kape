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
