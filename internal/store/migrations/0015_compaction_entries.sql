-- A compaction entry holds a summary that replaces the conversation before it,
-- except its newest messages, on every path through it.
ALTER TABLE session_entries DROP CONSTRAINT session_entries_kind_check;
ALTER TABLE session_entries ADD CONSTRAINT session_entries_kind_check
    CHECK (kind IN ('user', 'assistant', 'tool_call', 'tool_result', 'system', 'event', 'compaction'));
