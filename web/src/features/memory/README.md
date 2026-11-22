# memory

The Memory page: what Eika remembers about the user, read-only for now
(editing, pin, archive and purge are P5-14). It is a `MasterDetail` page
(`components/MasterDetail.tsx`): from 1024px the list and the open memory sit
side by side; under it the route `/memory/:name` picks one and the detail has
a back link.

- `MemoryPage.tsx` holds the filters and lays out the two panes.
- `MemoryList.tsx` has the search box (`GET /api/memories?q=`), type and state
  chips and a Pinned toggle, then a row per memory: name, type, a Pinned mark,
  and a Pending or Archived chip, over its description. A list pages with
  "Show more"; a search is one page.
- `MemoryDetail.tsx` shows one memory in full: chips, the description, why it
  is pending (flagged spans of its screen) or archived, dates, its source and
  the body in the shared `Markdown` renderer.
- `state.ts` is the pure part: filters to wire query (a search takes no
  `state` or `pinned`, so `narrow` applies them to the hits), labels and
  `sourceParts`, which links `session:<id>` to Activity for a subagent or Chat
  for the home session. An entry has no page, so it stays text.
- `queries.ts` is the server state. Nothing refreshes on its own yet: memory
  events arrive with P5-12.

Test it: `npm test -- memory/state` and `web/e2e/specs/memory.spec.ts`, which
drives the `memory` scenario (active, pending with a flagged span, archived,
pinned) on desktop and a coarse-pointer phone.
