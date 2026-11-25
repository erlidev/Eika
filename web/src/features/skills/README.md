# skills

The Skills page: the user's installed skills and the install from a zip. It is a
`MasterDetail` page (`components/MasterDetail.tsx`) at `/skills/:name?`; the rail lists it
and a phone finds it in the More sheet.

- `SkillsPage.tsx` lays out the two panes.
- `Installer.tsx` is the drop zone and the "Choose a zip" picker (one file; no other
  upload component takes a zip). The answer is shown under it: a new skill is disabled,
  with its warnings and screen hit count and a link to it; an unchanged reinstall says
  so; a 400, 413 or 503 is the server's message, plain.
- `SkillList.tsx` is a row per skill: name, Enabled or Disabled, Broken, warning and
  screen-hit counts, description.
- `SkillDetail.tsx` is what the user reads before enabling: state, the broken reason,
  warnings, an `unscanned` notice, the screen's hits (`Flagged in the description: ...`),
  the six spec fields, the file list with sizes and the `SKILL.md` text as source with
  the screen's spans marked (`lib/segments.ts`, UTF-8 byte offsets, shared with the
  Inbox). P6-11 adds enable, disable and delete.
- `state.ts` words the counts and the install answer. `queries.ts` is the server state.
  `events.ts` invalidates every skills query on `skills.changed`, `bus.dropped` and
  reconnect.

Test it: `npm test -- skills` and `web/e2e/specs/skills.spec.ts` (scenario `skills`).
