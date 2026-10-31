# sessions

The list of chats, which are sessions in no computer: create, list, rename,
pin, archive, delete, and the order the sidebar draws them in.
The session that is open is the `session` feature, not this one.

- `queries.ts` wraps `/api/sessions`. `useChats()` lists the chats, and
  `useCreateSession` opens one, untitled unless given a title.
  `useUpdateSession` renames, pins, or archives one. `useSessionUpdates`,
  called once by the workbench, refetches the lists when a `session.title`
  event says an untitled session was named or a `session.updated` event says
  someone changed one.
- `order.ts` is the order. `pinnedFirst` puts pinned chats first, and
  `splitArchived` takes the archived ones out for the sidebar's folded
  Archived heading.

Two features rather than one because the list is cheap and always mounted,
while the open session owns a stream subscription and a Zustand store.

Test it: `npm test -- sessions/order` covers the pinned order and the archived
split; `web/e2e/specs/organize.spec.ts` renames, pins, and archives in the
sidebar. The queries themselves are three calls over the API.
