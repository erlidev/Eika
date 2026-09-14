# settings

Deployment settings and the two choices that belong to the browser.

- `queries.ts` wraps `/api/settings` and `/api/models`. `default_model` is the
  one key the harness reads itself; `defaultModelOf` pulls it out of the table.
- `SettingsDialog.tsx` sets the default model, the colour scheme
  (`app/theme.ts`), and forgets the stored token.

The settings table holds arbitrary JSON, so `Settings` is
`Record<string, unknown>` and every reader narrows what it wants.

Test it: no unit test; it is two queries and a form.
