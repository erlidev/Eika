# inbox

The Inbox page (`/inbox`): what waits for the user's answer. Today that is
tripwire entries (P4b-09) and pending memories (P5-12 API); approvals and forms
arrive in later phases.

- `InboxPage.tsx` lists one card per pending memory, newest first, in a column
  capped at 40rem, with empty, loading and error states.
- `TripwireCard.tsx` is one tripwire that fired, listed above the memories: what
  happened in plain words, where the destination came from, whether the run is
  still held, and "This was fine" and "Stop the run" (offered only while the run
  is going; a 409 is said plainly and the card is read again). `state.ts` holds
  its titles and failure text.
- `MemoryCard.tsx` shows type, source links, the description and body with the
  screen's flagged spans marked, and Accept, Edit (then "Save and accept") and
  Reject. A refused answer shows its reason on the card and leaves the memory
  pending: 409 (stale or already answered), 400 (a credential) and 503.
- `state.ts` is the pure part: `answerFailure`. The byte-offset `segments` it used to hold is
  `lib/segments.ts`, shared with the skill detail.
- `queries.ts` is the server state. `useInboxEvents`, called once by the shell,
  keeps the badge (`inbox.count`) and the lists (`memory.changed`,
  `tripwire.changed`) current and
  reads both again on `bus.dropped` and reconnect. The badge itself is drawn by
  `app/Navigation.tsx`.

Test it: `npm test -- inbox/state` and `web/e2e/specs/inbox.spec.ts`
(scenario `inbox`).
