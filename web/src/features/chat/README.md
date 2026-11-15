# chat

The Chat page: Eika's one conversation, which is the home session.

- `ChatPage.tsx` reads the home session with `useHome` (`GET /api/home`) and
  renders `features/session`'s `SessionView` for its id, so the transcript, the
  composer, the run status bar and the stream are the session view's. While
  the home session loads it says so, and a failed read gets a Retry.
- `queries.ts` is the one query: the home session never changes identity, so
  nothing invalidates it.

The page has no list of chats and no way to start one: there is one
conversation. Pagination, the touch composer and the rest of the cards are
`PLAN.md` P3-05, P3-06 and P3-08.

Test it: `web/e2e/specs/chat.spec.ts` opens Chat on the mock harness; the
shell around it is `web/e2e/specs/shell.spec.ts`.
