-- Sentry's audit log: one row per decision that is not a plain allow, per
-- removal and per tripwire. Append-only. It holds no foreign keys, so a row
-- outlives the session or computer it names.
CREATE TABLE audit (
    id         text PRIMARY KEY,
    at         timestamptz NOT NULL DEFAULT now(),
    event      text NOT NULL CHECK (btrim(event) <> ''),
    session_id text NOT NULL DEFAULT '',
    run_id     text NOT NULL DEFAULT '',
    subagent   text NOT NULL DEFAULT '',
    routine    text NOT NULL DEFAULT '',
    computer   text NOT NULL DEFAULT '',
    key        text NOT NULL DEFAULT '',
    kind       text NOT NULL DEFAULT '',
    name       text NOT NULL DEFAULT '',
    digest     text NOT NULL DEFAULT '',
    reason     text NOT NULL DEFAULT '',
    detail     jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_at_idx ON audit (at DESC, id DESC);

CREATE FUNCTION audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit is append-only';
END $$;
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON audit
    FOR EACH ROW EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON audit
    FOR EACH STATEMENT EXECUTE FUNCTION audit_append_only();
