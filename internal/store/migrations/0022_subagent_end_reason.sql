-- waiting_reason also holds why a child failed or was cut off by a shutdown.
ALTER TABLE sessions DROP CONSTRAINT sessions_waiting_reason_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_waiting_reason_check
    CHECK (state IN ('waiting', 'failed', 'cancelled') OR waiting_reason = '');
