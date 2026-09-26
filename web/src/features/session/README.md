# session

The session that is open: its streaming transcript, its composer, its run, and
the three context panels it registers. A chat is a session with no workspace
and uses the same view; its header says it is a chat where a workspace
session's names its workspace. This is the largest feature and the only
one that holds client state of its own.

## Entry points

- `SessionView.tsx` is the centre pane. It owns the stream subscription and
  composes the transcript, the composer, and the run status bar under it.
- `useSessionStream.ts` folds every event of `session:<id>` into the store and
  asks for the replay that fills the transcript. A turn that ended, events the
  bus dropped, and a socket that reconnected all ask for the same catch-up:
  one replay from the last entry the transcript holds.
- `transcript.ts` is the reducer: a pure function from `(state, event)` to
  state, folding `docs/api/events.md` into renderable items. It knows two
  sources, the live turn keyed by `run_id` and the stored entries keyed by
  `entry_id`, and drops the live items of a turn once its entries arrive.
- `cutOffText` in `transcript.ts` turns a `length` or `content_filter` stop
  reason into the line the transcript shows under the answer. The harness
  keeps what a cut-off response produced (`docs/DECISIONS.md`), so without that
  line the answer would just stop and read as a bug.
- `Transcript.tsx` renders those items. It owns its scroller so it can tell
  whether the user is still at the bottom — it follows the stream only then,
  and offers "Jump to latest" otherwise — and every row is memoised on the
  item the reducer produced, so one delta re-renders one bubble. A message
  the user sent carries a rewind: it moves the head to the entry before it
  and puts its text back in the composer, which is how a question is edited
  and asked again. It is offered only while the session is idle and only
  where `rewindTarget` finds a resumable entry to land on.
- `Reasoning.tsx` is the model thinking: one quiet line that opens on a
  click, or open from the start when the reader asked for that in
  `preferences.ts`, the browser-local store of how the transcript renders.
- `ContextMeter.tsx` is what the status bar states about cost. Every number in
  it was measured by the endpoint and arrived in `turn.progress` or
  `turn.end`; assistant entries store the final context measurement, so
  replay restores the meter. The tooltips state the numbers and leave the
  explaining to the run panel, whose breakdown also repeats the two rates
  when the reader has turned them on.
- `Speed.tsx` is how fast an answer was produced, under the answer: the
  generation rate, and the prompt rate for an endpoint that measures its own
  (`docs/api/events.md` lists which do). It is off unless the reader turns it
  on in `preferences.ts`.
- `escape.ts` decides whether an `Esc` belongs to the run or to the dialog,
  select, palette, or text field on top of it.
- `store.ts` is a thin Zustand shell around that reducer. It owns which
  session is open, which model its next run uses — the status bar sets that
  and the run panel reads it, so neither can own it — and the composer's
  draft, which is here because rewinding a message puts it back in the box
  and the two components are not each other's parents. `rewound` discards the
  transcript and asks for the path again: a replay only ever adds, so without
  it the branch the head just left would stay on screen.
- `RunStatusBar.tsx` is the pane's footer, under the composer: the run's
  state, the model, the reasoning effort (one click cycles through the
  model's own list), the context meter, and the abort.
- `Composer.tsx` is the message box. Its buttons and key hints sit inside the
  box, and the box grows with the text up to a cap — without one a long
  message pushes the transcript out of the pane.
- `queries.ts` wraps the session, outline, run, message, head, and fork
  routes. `useSetSessionHead` rebuilds the transcript afterwards, and
  `useRewind` is the one a message's rewind calls: it lands the head on the
  entry before the message and then puts the text in the composer, in that
  order, so a refused move leaves nothing in the box.
- `ToolsPanel.tsx` is a chat's third panel, in place of a workspace's files,
  terminal, and changes: the profiles feature's `ToolPicker` over the
  session's own tool choice, with a reset back to its profile's, and the
  names of the tools a chat never has. `tools.ts` holds the pure parts: what
  a chat can never offer, and the one-line summary of a description written
  for the model.
- `ToolCard.tsx` draws one tool call and delegates to the renderer registry.
- `renderers/` is that registry: `registry.ts` has the type and the argument
  helpers, `renderers.tsx` maps a tool name to a renderer, `parts.tsx` holds
  the pieces they share. Adding one is in `docs/EXTENDING.md`.
- `SessionTreePanel.tsx` and `RunPanel.tsx` are the two panels `app/panels.tsx`
  registers. The run panel is where the status bar's meter explains itself:
  the window, the prompt and the response that fill it, and the turn's cost. The tree follows the WAI-ARIA tree pattern with flat rows
  (`aria-level`, `aria-posinset`, `aria-setsize`) laid out by `tree.ts`: one
  tab stop, arrows, Home and End to move, Enter to move the head. Nothing
  collapses, so no row has `aria-expanded`; the head is `aria-current`. A row
  whose entry is not `resumable` is in the middle of a turn: a run cannot
  continue from it, so its previews are muted and both its buttons are
  disabled, matching the `400` the harness answers.

## Why a store and not a query

The transcript is not server state: it is built from a stream, and a delta
arrives ten times a second. TanStack Query holds what the session _is_ (its
title, its outline, its run); the store holds what it is _saying_.

## Test it

`npm test -- transcript` folds the scripted event sequences from
`docs/api/events.md` through the reducer, the meter's arithmetic included. A
new event type or a new folding rule needs a case there. `npm test -- tree` covers the tree's rows, its
keyboard movement, and `rewindTarget`. `npm test -- session/tools` covers what a
chat cannot offer.
