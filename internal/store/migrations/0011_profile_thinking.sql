-- Whether a run replays the model's earlier reasoning, which a profile can
-- now set over the model's own switch. NULL is not set here: the model
-- row's preserve_thinking applies. A session's overrides carry the same key
-- in their JSON, so they need no column.
ALTER TABLE profiles ADD COLUMN preserve_thinking boolean;
