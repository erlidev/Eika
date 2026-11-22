# inbox

The Inbox page (`/inbox`): what waits for the user's answer. Today that is
pending memories (P5-12 API); approvals, tripwires and forms arrive in later
phases.

- `InboxPage.tsx` lists one card per pending memory, newest first, in a column
  capped at 40rem, with empty, loading and error states.
- `MemoryCard.tsx` shows type, source links, the description and body with the
  screen's flagged spans marked, and Accept, Edit (then "Save and accept") and
  Reject. A refused answer shows its reason on the card and leaves the memory
  pending: 409 (stale or already answered), 400 (a credential) and 503.
- `state.ts` is the pure part: `segments` (spans are UTF-8 byte offsets) and
  `answerFailure`.
- `queries.ts` is the server state. `useInboxEvents`, called once by the shell,
  keeps the badge (`inbox.count`) and the list (`memory.changed`) current and
  reads both again on `bus.dropped` and reconnect. The badge itself is drawn by
  `app/Navigation.tsx`.

Test it: `npm test -- inbox/state` and `web/e2e/specs/inbox.spec.ts`
(scenario `inbox`).
