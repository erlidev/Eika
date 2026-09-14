# sessions

The list of sessions in a workspace: create, list, delete. The session that is
open is the `session` feature, not this one.

- `queries.ts` wraps `/api/sessions`. `useSessions(workspaceId?)` lists them.

Two features rather than one because the list is cheap and always mounted,
while the open session owns a stream subscription and a Zustand store.

Test it: no unit test; it is three queries over the API.
