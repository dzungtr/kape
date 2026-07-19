DROP TRIGGER IF EXISTS immutable_terminal_tasks ON tasks;
DROP FUNCTION IF EXISTS prevent_terminal_update();
REVOKE INSERT ON tasks FROM kape_writer;
DROP ROLE IF EXISTS kape_writer;
