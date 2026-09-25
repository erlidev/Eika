-- The request field that turns a model's thinking off when its effort is
-- "none". Endpoints disagree on the field, and one that does not know a field
-- may reject the whole request, so each model names the one its endpoint
-- understands: the standard reasoning_effort, or the chat_template_kwargs or
-- thinking extension.
ALTER TABLE models
    ADD COLUMN thinking_switch text NOT NULL DEFAULT 'reasoning_effort'
        CHECK (thinking_switch IN ('reasoning_effort', 'chat_template_kwargs', 'thinking'));
