-- A worktree workspace has no container of its own: it is a git worktree in
-- the container of the workspace worktree_of names, which is how a child
-- agent works beside its parent. The worktree lives in the holder's volume,
-- so it goes when the holder does. NULL is a workspace with its own container.
ALTER TABLE workspaces
    ADD COLUMN worktree_of text REFERENCES workspaces (id) ON DELETE CASCADE;

CREATE INDEX workspaces_worktree_of_idx ON workspaces (worktree_of);
