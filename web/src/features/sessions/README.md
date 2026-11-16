# sessions

What concerns sessions as a set rather than the one that is open: the home
computer, renaming, pinning, and archiving a session, and keeping every cached
session current. The session that is open is the `session` feature, not this
one.

- `queries.ts` wraps `/api/sessions` and `/api/computers`. `useHomeComputer()`
  finds the computer the harness makes for itself. `useUpdateSession` renames,
  pins, or archives a session. `useSessionUpdates`, called once by the shell,
  refetches the session queries when a `session.title` event says an untitled
  session was named or a `session.updated` event says someone changed one, and
  the computers when a `computer.state` event on the global topic says the
  harness recorded the home computer. A reconnect or a `bus.dropped` refetches
  every session query, since the event that would have said what changed is
  gone; Activity's subagent board relies on it.

Two features rather than one because these are cheap and always mounted,
while the open session owns a stream subscription and a Zustand store.

Test it: the queries are calls over the API with no test of their own;
`web/e2e/specs/activity.spec.ts` follows a `session.updated` to the board.
