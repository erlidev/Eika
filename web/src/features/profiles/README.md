# profiles

Agent profiles, as the UI edits them. A profile is a named configuration of
what a run sends; a session runs with one and can override it. The harness
resolves the layers and says where each value came from, so nothing here
re-derives the rule: an editor shows what an unset field falls through to
from the `inherited` configuration the API returns.

- `ProfilesSettings` is the Profiles tab of the settings dialog: the list,
  the default profile, and each profile's editor.
- `SettingsEditor` is the editor of one layer, with Prompt, Tools, and
  Sampling tabs; its rules (the draft, parsing, tool choices by server) are
  `form.ts`.
- `SessionProfile` is the status bar's profile button and the dialog it
  opens over the session's profile, overrides, and tool choice.
- `queries.ts` holds the profile and session configuration queries.

Test: `npm test -- src/features/profiles` for the rules, and
`e2e/specs/profiles.spec.ts` against the mock harness.
