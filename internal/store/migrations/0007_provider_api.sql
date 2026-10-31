-- The wire protocol Core speaks to a provider's endpoint (docs/design/
-- runtime.md section 1, "Responses or Chat Completions"): chat for Chat
-- Completions, responses for the stateless Responses API.
--
-- api holds the value in force. A probe sets it from the Responses
-- conformance check, whose outcome is in capabilities, and leaves it alone
-- when the check has no reason to run again; a user's own choice is named in
-- overridden, like backend, slots and kv, and the next probe leaves it alone.
-- Every provider that exists stays on chat until a check shows otherwise.

ALTER TABLE providers
    ADD COLUMN api text NOT NULL DEFAULT 'chat'
        CHECK (api IN ('responses', 'chat'));
