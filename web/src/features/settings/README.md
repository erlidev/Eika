# settings

Everything the user configures after setup, in one dialog of tabs.

- `SettingsDialog.tsx` is the dialog. Its open state and tab live in
  `store.ts`, so the navigation and a session's "no model" notice can both
  open it on the right tab.
- Models is `features/providers/ModelsPanel`. General is `GeneralSettings.tsx`:
  `ModelClasses.tsx` (a model per class, with the
  fallback each class has and the classes that share a pool, from
  `classes.ts`; it lists models with `ModelSelectItems.tsx`), the image the
  home computer is created from, with `SystemCheck.tsx`, and a way back into the guided setup. Computer is
  `ComputerSettings.tsx`: the home computer's limits and network, which a
  save applies to its container, with the fields from `features/sandbox`; `sandboxDefaults` in `queries.ts`
  reads them from the table. Search is
  `SearchSettings.tsx`: the web provider order, the search API keys, the
  quotas, the sources' health, and a box to try a search, over
  `features/search`. Account is
  `AccountSettings.tsx`: the password and signing out. Appearance is the
  colour scheme from `app/theme.ts`, the one setting kept in the browser.
  Agents is `features/agents`; Instructions is `InstructionsSettings.tsx`: the
  user's "About me" and rules, saved together with `PUT /api/instructions`, with the
  harness's text limit checked beside each field (`lib/textLimit.ts`).
- Security is `SecuritySettings.tsx`: the withheld list from `GET /api/withheld`, newest
  first and paged, each row with its source, where it came from and the removed text. A
  tool card whose result had a removal carries a `withheld` mark, read from `removed` on its
  `tool.result` event or stored entry (`features/session/ToolCard.tsx`).
- Developer is `DeveloperSettings.tsx`: select a session and inspect its next
  request or a recorded call, including token counts and drift.
- `queries.ts` wraps `/api/settings` and `/api/system`. `settingKeys` names
  the keys the harness reads itself and validates; any other key is stored
  as it comes. `setupComplete` is what the app checks before the shell.

The settings table holds arbitrary JSON, so `Settings` is
`Record<string, unknown>` and every reader narrows what it wants with
`settingString`.

Every form says what keeps it from saving next to the field at fault, and
every failed read shows `LoadError` from `components/Notice` with a Retry.

Test it: the rules the forms use are tested where they live
(`features/search/search.test.ts` for the quotas); the forms themselves are
checked with `make dev` and the visual specs in `web/e2e`.
