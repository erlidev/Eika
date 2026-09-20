# settings

Everything the user configures after setup, in one dialog with five tabs.

- `SettingsDialog.tsx` is the dialog. Its open state and tab live in
  `store.ts`, so the workbench, the command palette, and a session's "no
  model" notice can all open it on the right tab.
- Models is `features/providers/ModelsPanel`. General is `GeneralSettings.tsx`:
  `DefaultModelSelect.tsx`, the sandbox image with `SystemCheck.tsx`, the
  subagent limits, and a way back into the guided setup. Search is
  `SearchSettings.tsx`: the web provider order, the search API keys, the
  quotas, the sources' health, and a box to try a search, over
  `features/search`. Account is
  `AccountSettings.tsx`: the password and signing out. Appearance is the
  colour scheme from `app/theme.ts`, the one setting kept in the browser.
- `queries.ts` wraps `/api/settings` and `/api/system`. `settingKeys` names
  the keys the harness reads itself and validates; any other key is stored
  as it comes. `setupComplete` is what the app checks before the workbench.

The settings table holds arbitrary JSON, so `Settings` is
`Record<string, unknown>` and every reader narrows what it wants with
`settingString` and `settingNumber`.

Every form says what keeps it from saving next to the field at fault, and
every failed read shows `LoadError` from `components/Notice` with a Retry.

Test it: the rules the forms use are tested where they live
(`features/search/search.test.ts` for the quotas); the forms themselves are
checked with `make dev` and the visual specs in `web/e2e`.
