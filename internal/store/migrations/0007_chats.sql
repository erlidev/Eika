-- A chat is a session with no workspace. Its runs offer the model only the
-- tools that need none, so a session's workspace becomes optional; a
-- workspace's own sessions still cascade with it and never become chats.
ALTER TABLE sessions ALTER COLUMN workspace_id DROP NOT NULL;

-- The tools a session's runs may offer the model. NULL is every tool the
-- session can run; an array is the ones the user chose, possibly none.
ALTER TABLE sessions ADD COLUMN tools text[];

-- The sidebar lists the chats apart from every workspace's sessions.
CREATE INDEX sessions_chat_idx ON sessions (created_at) WHERE workspace_id IS NULL;
