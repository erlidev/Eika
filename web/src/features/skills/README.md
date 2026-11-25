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
- `SkillDetail.tsx` is a placeholder (description and counts); P6-10 adds the full
  `SKILL.md` with marked spans and P6-11 enable, disable and delete.
- `state.ts` words the counts and the install answer. `queries.ts` is the server state.
  `events.ts` invalidates every skills query on `skills.changed`, `bus.dropped` and
  reconnect.

Test it: `npm test -- skills` and `web/e2e/specs/skills.spec.ts` (scenario `skills`).
