-- model_requests becomes model_calls and carries the accounting of
-- docs/design/runtime.md section 2, "Terms": what the runtime predicted
-- before the call, what the server measured, and who made the call.
--
-- The two counts keep their data and take the design's names: input_tokens is
-- prompt_tokens (the prompt the server tokenised, cached part included) and
-- output_tokens is completion_tokens (everything generated, reasoning
-- included). total_tokens stays as the server stated it.
--
-- New columns, all nullable: NULL is "not known", never 0, and an aggregate
-- over them skips what is NULL rather than counting it as nothing.
--   prefilled_tokens        tokens the server processed: timings.prompt_n, or
--                           the prompt less the cached part when both are
--                           stated.
--   reasoning_tokens        the part of the completion that was reasoning.
--   reasoning_source        where reasoning_tokens came from: 'reported' is
--                           the server's own count, 'tokenised' is a count of
--                           the reasoning text by the endpoint's tokenizer,
--                           which approximates what was generated. NULL
--                           exactly when reasoning_tokens is.
--   predicted_prompt_tokens, prediction
--                           the token counter's expectation of the prompt
--                           before the call, and its method (exact, anchored
--                           or estimated). NULL when nothing was predicted.
--   drift                   predicted_prompt_tokens less prompt_tokens, NULL
--                           when either is.
--   context_window          the window the call was judged against, NULL when
--                           the model has none configured. The design calls
--                           it window, which is a reserved word.
--   endpoint                the provider's name when the call was made.
--
-- Columns with no source yet, left NULL until the item that produces them
-- fills them (the inference queue for queue_ms, slot, class and priority; the
-- agents item for agent): nothing writes them today, and they exist so that
-- those items add no migration of their own.
--   queue_ms, slot, class, priority, agent
--
-- A stored call predicted nothing, so its prediction columns are NULL.

ALTER TABLE model_requests RENAME TO model_calls;

ALTER TABLE model_calls RENAME COLUMN input_tokens TO prompt_tokens;
ALTER TABLE model_calls RENAME COLUMN output_tokens TO completion_tokens;

ALTER INDEX model_requests_session_idx RENAME TO model_calls_session_idx;
ALTER INDEX model_requests_run_idx RENAME TO model_calls_run_idx;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_pkey TO model_calls_pkey;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_session_id_fkey TO model_calls_session_id_fkey;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_run_id_fkey TO model_calls_run_id_fkey;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_model_id_fkey TO model_calls_model_id_fkey;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_message_tokens_check TO model_calls_message_tokens_check;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_input_tokens_check TO model_calls_prompt_tokens_check;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_output_tokens_check TO model_calls_completion_tokens_check;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_total_tokens_check TO model_calls_total_tokens_check;
ALTER TABLE model_calls RENAME CONSTRAINT model_requests_cached_tokens_check TO model_calls_cached_tokens_check;

ALTER TABLE model_calls
    ADD COLUMN prefilled_tokens        integer CHECK (prefilled_tokens >= 0),
    ADD COLUMN reasoning_tokens        integer CHECK (reasoning_tokens >= 0),
    ADD COLUMN reasoning_source        text CHECK (reasoning_source IN ('reported', 'tokenised')),
    ADD COLUMN predicted_prompt_tokens integer CHECK (predicted_prompt_tokens >= 0),
    ADD COLUMN prediction              text CHECK (prediction IN ('exact', 'anchored', 'estimated')),
    ADD COLUMN drift                   integer,
    ADD COLUMN context_window          integer CHECK (context_window > 0),
    ADD COLUMN endpoint                text,
    ADD COLUMN queue_ms                integer CHECK (queue_ms >= 0),
    ADD COLUMN slot                    integer CHECK (slot >= 0),
    ADD COLUMN class                   text,
    ADD COLUMN priority                text,
    ADD COLUMN agent                   text,
    ADD CONSTRAINT model_calls_reasoning_source_pair
        CHECK ((reasoning_tokens IS NULL) = (reasoning_source IS NULL)),
    ADD CONSTRAINT model_calls_prediction_pair
        CHECK ((predicted_prompt_tokens IS NULL) = (prediction IS NULL));
