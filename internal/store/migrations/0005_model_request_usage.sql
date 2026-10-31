-- A model call's token counts are NULL when the endpoint did not state them,
-- never 0 (docs/design/runtime.md section 2, "Terms"). The three counts were
-- NOT NULL DEFAULT 0, so a call that stated nothing read as a call that cost
-- nothing; they become nullable, and cached_tokens joins them so the count of
-- prompt tokens served from the endpoint's cache is not lost.
--
-- A stored call whose three counts are all 0 stated nothing: no call has an
-- empty prompt, since the template alone adds tokens. Those become NULL.
-- Every other stored count was stated and is kept as it is.

ALTER TABLE model_requests
    ALTER COLUMN input_tokens DROP NOT NULL,
    ALTER COLUMN input_tokens DROP DEFAULT,
    ALTER COLUMN output_tokens DROP NOT NULL,
    ALTER COLUMN output_tokens DROP DEFAULT,
    ALTER COLUMN total_tokens DROP NOT NULL,
    ALTER COLUMN total_tokens DROP DEFAULT,
    ADD COLUMN cached_tokens integer CHECK (cached_tokens >= 0);

UPDATE model_requests
    SET input_tokens = NULL, output_tokens = NULL, total_tokens = NULL
    WHERE input_tokens = 0 AND output_tokens = 0 AND total_tokens = 0;
