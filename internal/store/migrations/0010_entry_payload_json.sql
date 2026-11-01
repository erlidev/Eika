-- An entry's payload is kept as the text it was written as (docs/DECISIONS.md,
-- "Prefix stability").
--
-- jsonb parses a document and writes it back in its own form: keys ordered by
-- length and then bytes, a space after every colon and comma. A message read
-- back from jsonb is not the bytes that were sent to the model when it was
-- new, so at the start of every run the arguments of each earlier tool call
-- (and the reasoning items of a Responses endpoint) were a different prefix
-- from the one the server's prompt cache held. json stores the text as it is.
-- Nothing queries inside a payload, so no operator of jsonb is lost.

ALTER TABLE session_entries ALTER COLUMN payload TYPE json USING payload::json;
