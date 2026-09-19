-- Web search: the API keys of the keyed search providers and GitHub, sealed
-- by internal/secret like every other credential, and each quota bucket's
-- usage, so that a monthly quota survives a restart.

CREATE TABLE search_keys (
    name       text PRIMARY KEY,
    key        bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- day and month are the UTC day ("2026-09-18") and month ("2026-09") the
-- counters count in; a row from an earlier day reads as zero for that day.
CREATE TABLE search_usage (
    name           text PRIMARY KEY,
    day            text NOT NULL,
    day_used       integer NOT NULL CHECK (day_used >= 0),
    month          text NOT NULL,
    month_used     integer NOT NULL CHECK (month_used >= 0),
    cooldown_until timestamptz,
    fail_streak    integer NOT NULL DEFAULT 0 CHECK (fail_streak >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
