ALTER TABLE sessions ADD COLUMN waiting_reason text NOT NULL DEFAULT '';
ALTER TABLE sessions ADD CONSTRAINT sessions_waiting_reason_check
    CHECK (state = 'waiting' OR waiting_reason = '');
