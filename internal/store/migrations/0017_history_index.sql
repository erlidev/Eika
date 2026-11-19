-- Keep the original JSON intact. Bound index input to keep the vector below
-- PostgreSQL's 1 MB limit; text beyond the prefix remains in the payload.
-- JSON permits escaped NULs, which PostgreSQL text cannot hold. Replace them
-- only in the copy used for indexing, before extracting any JSON strings.
CREATE FUNCTION history_vector(kind text, source json) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT to_tsvector('english', left(CASE kind
        WHEN 'user' THEN coalesce(payload->>'content', '')
        WHEN 'assistant' THEN coalesce(payload->>'content', '') || ' ' || coalesce((
            SELECT string_agg(coalesce(call->>'name', '') || ' ' || coalesce(call->>'arguments', ''), ' ')
            FROM json_array_elements(CASE WHEN json_typeof(payload->'tool_calls') = 'array'
                THEN payload->'tool_calls' ELSE '[]'::json END) AS call), '')
        WHEN 'tool_call' THEN coalesce(payload->>'name', '') || ' ' || coalesce(payload->>'arguments', '')
        WHEN 'notice' THEN coalesce(payload->>'text', '')
        WHEN 'compaction' THEN coalesce(payload->>'summary', '')
        ELSE '' END, 32000))
    FROM (SELECT replace(source::text, E'\\u0000', E'\\u0020')::json AS payload) safe
$$;
ALTER TABLE session_entries ADD COLUMN tsv tsvector
    GENERATED ALWAYS AS (history_vector(kind, payload)) STORED;
CREATE INDEX session_entries_tsv_idx ON session_entries USING gin (tsv);
