CREATE TABLE routines (
    id text PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT false,
    schedule_kind text NOT NULL CHECK (schedule_kind IN ('at', 'cron')),
    schedule_expr text NOT NULL,
    timezone text NOT NULL,
    agent text NOT NULL,
    project_id text REFERENCES projects (id) ON DELETE RESTRICT,
    prompt text NOT NULL,
    max_runtime_seconds bigint NOT NULL DEFAULT 86400 CHECK (max_runtime_seconds > 0),
    missed text NOT NULL DEFAULT 'run_once' CHECK (missed = 'run_once'),
    grace_seconds bigint NOT NULL DEFAULT 21600 CHECK (grace_seconds >= 0),
    created_by text NOT NULL,
    approved_at timestamptz,
    next_run_at timestamptz,
    last_run_at timestamptz,
    last_outcome text NOT NULL DEFAULT '',
    last_reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX routines_due_idx ON routines (next_run_at) WHERE enabled AND approved_at IS NOT NULL;
CREATE TABLE routine_runs (
    id text PRIMARY KEY,
    routine_id text NOT NULL REFERENCES routines (id) ON DELETE RESTRICT,
    session_id text REFERENCES sessions (id) ON DELETE RESTRICT,
    scheduled_at timestamptz NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('running', 'done', 'error', 'skipped', 'interrupted')),
    reason text NOT NULL DEFAULT '',
    tokens bigint,
    duration_ms bigint,
    started_at timestamptz NOT NULL,
    finished_at timestamptz
);
CREATE INDEX routine_runs_routine_idx ON routine_runs (routine_id, started_at DESC);
CREATE UNIQUE INDEX routine_runs_running_idx ON routine_runs (routine_id) WHERE outcome = 'running';
CREATE TABLE wakeups (
    id text PRIMARY KEY,
    session_id text NOT NULL REFERENCES sessions (id) ON DELETE RESTRICT,
    agent text NOT NULL,
    note text NOT NULL,
    due_at timestamptz NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'claimed', 'done', 'cancelled'))
);
CREATE INDEX wakeups_due_idx ON wakeups (due_at) WHERE state = 'pending';
WITH seed AS (
    SELECT COALESCE((SELECT value #>> '{}' FROM settings
        WHERE key = 'time_zone' AND EXISTS
            (SELECT 1 FROM pg_timezone_names WHERE name = value #>> '{}')), 'UTC') AS zone
)
INSERT INTO routines (id, name, description, schedule_kind, schedule_expr, timezone, agent, prompt, created_by, next_run_at)
SELECT 'shipped-morning-brief', 'Morning brief', 'A weekday morning briefing.',
    'cron', '30 7 * * mon-fri', zone, 'eika', 'Prepare a morning brief for the user.', 'user',
    (SELECT min((d::date + time '07:30') AT TIME ZONE zone)
     FROM generate_series((now() AT TIME ZONE zone)::date,
                          (now() AT TIME ZONE zone)::date + 8, interval '1 day') d
     WHERE EXTRACT(ISODOW FROM d) <= 5 AND (d::date + time '07:30') AT TIME ZONE zone > now())
FROM seed
ON CONFLICT (id) DO NOTHING;
