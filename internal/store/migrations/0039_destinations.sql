-- The local destination index (docs/design/sentry.md, section 3): where each
-- host first appeared in a session, and the hosts that count as known. Phase 11
-- extends it with grants and approved sends.
CREATE TABLE destinations (
    session_id text NOT NULL,
    host text NOT NULL,
    source text NOT NULL,
    source_name text NOT NULL DEFAULT '',
    at timestamptz NOT NULL,
    PRIMARY KEY (session_id, host)
);

-- computer_id is empty for a host known to every computer.
CREATE TABLE known_hosts (
    computer_id text NOT NULL DEFAULT '',
    host text NOT NULL,
    reason text NOT NULL,
    at timestamptz NOT NULL,
    PRIMARY KEY (computer_id, host)
);
