CREATE ROLE kape_writer;
GRANT INSERT ON tasks TO kape_writer;

CREATE OR REPLACE FUNCTION prevent_terminal_update()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('Completed', 'Failed', 'SchemaValidationFailed', 'ActionError', 'UnprocessableEvent', 'Timeout', 'Retried') THEN
        RAISE EXCEPTION 'task % is in terminal state % and cannot be updated', OLD.id, OLD.status
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER immutable_terminal_tasks
BEFORE UPDATE ON tasks
FOR EACH ROW EXECUTE FUNCTION prevent_terminal_update();
