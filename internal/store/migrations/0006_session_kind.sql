-- A session's kind says who opened it: the user, a fork of another session,
-- or a subagent a run spawned. The user interface draws a fork and a child
-- agent under the session they came from, and `parent_session_id` alone does
-- not tell the two apart.
--
-- The kind is a column rather than a join on `subagents`, because it is what
-- the session is, not what some other row says about it: a subagent row that
-- is gone must not turn a child agent's session back into a fork.
ALTER TABLE sessions
    ADD COLUMN kind text NOT NULL DEFAULT 'user'
        CHECK (kind IN ('user', 'fork', 'agent'));

UPDATE sessions SET kind = 'fork' WHERE parent_session_id IS NOT NULL;

UPDATE sessions SET kind = 'agent'
WHERE id IN (SELECT child_session_id FROM subagents);
