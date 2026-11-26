ALTER TABLE routines ADD COLUMN warning jsonb NOT NULL DEFAULT '{"spans":[],"max_score":0,"unscanned":true}'::jsonb;
ALTER TABLE routines ADD COLUMN revision bigint NOT NULL DEFAULT 0;
ALTER TABLE routines ADD COLUMN deleted_at timestamptz;
