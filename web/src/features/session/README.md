# session

The session that is open: its streaming transcript, its composer, its run, and
the two context panels it registers. This is the largest feature and the only
one that holds client state of its own.

## Entry points

- `SessionView.tsx` is the centre pane. It owns the stream subscription and
  composes the transcript, the run status bar, and the composer.
- `useSessionStream.ts` folds every event of `session:<id>` into the store and
  asks for the replay that fills the transcript. A turn that ended, events the
  bus dropped, and a socket that reconnected all ask for the same catch-up:
  one replay from the last entry the transcript holds.
- `transcript.ts` is the reducer: a pure function from `(state, event)` to
  state, folding `docs/api/events.md` into renderable items. It knows two
  sources, the live turn keyed by `run_id` and the stored entries keyed by
  `entry_id`, and drops the live items of a turn once its entries arrive.
- `Transcript.tsx` renders those items. It owns its scroller so it can tell
  whether the user is still at the bottom — it follows the stream only then,
  and offers "Jump to latest" otherwise — and every row is memoised on the
  item the reducer produced, so one delta re-renders one bubble.
- `escape.ts` decides whether an `Esc` belongs to the run or to the dialog,
  select, palette, or text field on top of it.
- `store.ts` is a thin Zustand shell around that reducer. The store owns which
  session is open and nothing else, so the folding rules stay testable alone.
- `queries.ts` wraps the session, outline, run, message, head, and fork routes.
- `ToolCard.tsx` draws one tool call and delegates to the renderer registry.
- `renderers/` is that registry: `registry.ts` has the type and the argument
  helpers, `renderers.tsx` maps a tool name to a renderer, `parts.tsx` holds
  the pieces they share. Adding one is in `docs/EXTENDING.md`.
- `SessionTreePanel.tsx` and `RunPanel.tsx` are the two panels `app/panels.tsx`
  registers.

## Why a store and not a query

The transcript is not server state: it is built from a stream, and a delta
arrives ten times a second. TanStack Query holds what the session _is_ (its
title, its outline, its run); the store holds what it is _saying_.

## Test it

`npm test -- transcript` folds the scripted event sequences from
`docs/api/events.md` through the reducer. A new event type or a new folding
rule needs a case there.
