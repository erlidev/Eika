-- How the user has sorted their sessions and workspaces: a pinned one is
-- listed before the others, an archived one is set aside under an Archived
-- heading. Both are the user's filing and change nothing about what a
-- session runs or what a workspace's container does.
ALTER TABLE sessions
    ADD COLUMN pinned boolean NOT NULL DEFAULT false,
    ADD COLUMN archived boolean NOT NULL DEFAULT false;

ALTER TABLE workspaces
    ADD COLUMN pinned boolean NOT NULL DEFAULT false,
    ADD COLUMN archived boolean NOT NULL DEFAULT false;
