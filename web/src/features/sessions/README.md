# sessions

The list of sessions in a workspace: create, list, delete, and the shape the
sidebar draws them in. The session that is open is the `session` feature, not
this one.

- `queries.ts` wraps `/api/sessions`. `useSessions(workspaceId?, descendants?)`
  lists them; `descendants` also brings back the forks and child agents those
  sessions led to, which run in workspaces of their own.
- `tree.ts` is the shape. `sessionTree` nests each session's forks and child
  agents under it, so the sidebar draws nested lists and the relationship is
  in the markup rather than only in the indent. `agentWorkspaces` names the
  workspaces that hold sessions and none of the user's: each is reached
  through the session that owns it, so the project's workspace list leaves it
  out rather than listing the same thing twice.

Two features rather than one because the list is cheap and always mounted,
while the open session owns a stream subscription and a Zustand store.

Test it: `npm test -- sessions/tree` covers the nesting and which workspaces
are hidden. The queries themselves are three calls over the API.
