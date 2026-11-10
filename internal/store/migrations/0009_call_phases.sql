-- How long a model call spent in each phase (docs/design/runtime.md section 2).
--
--   prefill_ms   the time the endpoint took to read the prompt, as it stated
--                it (llama.cpp's timings.prompt_ms). NULL when it states none.
--   decode_ms    the time spent generating: the endpoint's own measure when it
--                states one, otherwise the harness's, from the first streamed
--                token to the last. NULL when nothing measured it.
--
-- queue_ms, slot, class and priority already exist, and the inference queue now
-- fills them; a call recorded before it has them NULL.

ALTER TABLE model_calls
    ADD COLUMN prefill_ms integer CHECK (prefill_ms >= 0),
    ADD COLUMN decode_ms  integer CHECK (decode_ms >= 0);
