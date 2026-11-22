# memory

The Memory page: what Eika remembers about the user, with an editor. It is a `MasterDetail` page
(`components/MasterDetail.tsx`): from 1024px the list and the open memory sit
side by side; under it the route `/memory/:name` picks one and the detail has
a back link.

- `MemoryPage.tsx` holds the filters and lays out the two panes.
- `MemoryList.tsx` has the search box (`GET /api/memories?q=`), type and state
  chips and a Pinned toggle, then a row per memory: name, type, a Pinned mark,
  and a Pending or Archived chip, over its description. A list pages with
  "Show more"; a search is one page.
- `MemoryDetail.tsx` shows one memory: chips, why it is pending (flagged spans
  of its screen) or archived, dates and its source, over the editor.
- `MemoryEditor.tsx` is the form (description, type, expiry, body in the shared
  `MarkdownField`) with Save, Pin, Archive and a confirmed Purge. A save carries
  the sha it read; a stale 409 shows the current version and lets the user keep
  their edits (rebase) or take the current one. Credential, pending and 503 are
  said plainly.
- `MemoryPage.tsx` also has the Instructions tab (`/memory?tab=instructions`), which
  renders the settings dialog's `InstructionsSettings`; Settings keeps it too.
- `state.ts` is the pure part: filters to wire query (a search takes no
  `state` or `pinned`, so `narrow` applies them to the hits), labels and
  `sourceParts`, which links `session:<id>` to Activity for a subagent or Chat
  for the home session. An entry has no page, so it stays text.
- `queries.ts` is the server state. `events.ts` invalidates every
  memory query on `memory.changed`, on `bus.dropped` and on reconnect.

Test it: `npm test -- memory` and `web/e2e/specs/memory.spec.ts`, which
drives the `memory` scenario (active, pending with a flagged span, archived,
pinned) on desktop and a coarse-pointer phone.
