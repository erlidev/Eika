-- What a workspace's container may consume, reach, and expose: its CPU,
-- memory, and process limits, its egress mode and allowlist, and the ports
-- the harness forwards to it. It is the desired state, applied when the
-- workspace starts and whenever it changes. The empty object is a workspace
-- from before the column: no limits, open egress, and no ports.
ALTER TABLE workspaces ADD COLUMN sandbox jsonb NOT NULL DEFAULT '{}';
