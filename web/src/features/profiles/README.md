# profiles

Agent profiles, as the UI edits them. A profile is a named configuration of
what a run sends; a session runs with one and can override it. The harness
resolves the layers and says where each value came from, so nothing here
re-derives the rule: an editor shows what an unset field falls through to
from the `inherited` configuration the API returns.

- `ProfilesSettings` is the Profiles tab of the settings dialog: the list,
  the default profile, and each profile's editor.
- `SettingsEditor` is the editor of one layer, with Model, Prompt, Tools,
  and Sampling tabs under a strip that says what the draft costs every
  request. Its rules (the draft, parsing, sliders, tool choices by server)
  are `form.ts`.
- `fields.tsx` is what every field is built from: the header whose chip says
  where the value comes from, the reset, and the segmented choice that can
  be left unset. `SamplingControls.tsx` has the parameters' controls,
  `PromptEditor.tsx` a replaceable prompt with its diff against the
  built-in one.
- `ToolPicker` is the one control that chooses tools, used here and by a
  chat's Tools panel.
- `SessionProfile` is the status bar's profile button and the dialog it
  opens over the session's profile, overrides, and tool choice.
- `store.ts` says which editor is open and on which tab, so the Context
  inspector's Edit links can open one.
- `queries.ts` holds the profile and session configuration queries.

Test: `npm test -- src/features/profiles` for the rules, and
`e2e/specs/profiles.spec.ts` against the mock harness.
