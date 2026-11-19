-- Keep the user's profile settings and session choices recoverable without
-- leaving them in the runtime configuration path. These are snapshots, so
-- later model or session deletion cannot change the saved values.
CREATE TABLE retired_profiles AS TABLE profiles;
CREATE TABLE retired_session_profiles AS
    SELECT id AS session_id, kind AS original_kind, profile_id, overrides, tools
    FROM sessions;
CREATE TABLE retired_profile_defaults AS
    SELECT key, value FROM settings WHERE key = 'default_profile';
DELETE FROM settings WHERE key = 'default_profile';

ALTER TABLE sessions DROP COLUMN profile_id, DROP COLUMN overrides, DROP COLUMN tools;
DROP TABLE profiles;
DROP INDEX sessions_chat_idx;

-- Old direct conversations remain separate and keep their IDs, computers,
-- trees and titles. A routine is the non-home conversation kind; giving each
-- one an agent and a name makes its history accessible under the new shape.
UPDATE sessions SET kind = 'routine', agent = 'eika',
    name = COALESCE(NULLIF(btrim(title), ''), id)
WHERE kind = 'user';
ALTER TABLE sessions DROP CONSTRAINT sessions_kind_check;
ALTER TABLE sessions DROP CONSTRAINT sessions_identity_check;
ALTER TABLE sessions ALTER COLUMN kind DROP DEFAULT;
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_check
    CHECK (kind IN ('home', 'subagent', 'routine'));
ALTER TABLE sessions ADD CONSTRAINT sessions_identity_check
    CHECK (btrim(agent) <> '' AND btrim(name) <> '');

-- Entries are immutable history. Keep their original kind and JSON bytes,
-- but reject new entries of kinds the runtime no longer writes.
ALTER TABLE session_entries DROP CONSTRAINT session_entries_kind_check;
ALTER TABLE session_entries ADD CONSTRAINT session_entries_kind_check
    CHECK (kind IN ('user', 'assistant', 'tool_result', 'compaction', 'preamble', 'notice')) NOT VALID;
