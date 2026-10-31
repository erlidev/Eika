-- Model kinds and classes (docs/design/runtime.md section 1).
--
-- kind says what a model answers: chat for the models that exist today, and
-- decision for a System One model that answers typed questions at
-- /v1/systemone and cannot hold a conversation. A model's kind is chosen when
-- it is added and does not change, so a decision model can never have become
-- the default, a profile's or a session's chat model.
ALTER TABLE models
    ADD COLUMN kind text NOT NULL DEFAULT 'chat'
        CHECK (kind IN ('chat', 'decision'));

-- model_classes replaces utility_models. The one task there, the session
-- title, runs on the fast class, so a model the user assigned it becomes the
-- fast model. A user who assigned none, or an empty name, has no class set and
-- titles stay off, as they were.
INSERT INTO settings (key, value)
SELECT 'model_classes', jsonb_build_object('fast', value ->> 'session_title')
FROM settings
WHERE key = 'utility_models'
  AND jsonb_typeof(value) = 'object'
  AND coalesce(value ->> 'session_title', '') <> '';

DELETE FROM settings WHERE key = 'utility_models';
