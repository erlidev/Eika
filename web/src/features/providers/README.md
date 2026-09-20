# providers

The model providers and models agents run on. A provider is an
OpenAI-compatible endpoint and its key; a model is one of the models it
serves, with the limits and reasoning settings a run uses.

- `queries.ts` wraps `/api/providers` and `/api/models`, including the probe
  (which models does an endpoint serve) and the one-request model test. Every
  change invalidates the lists, the system counts, and the settings, which
  name the default model.
- `ProviderForm.tsx` connects a provider: a preset from `presets.ts` or a
  typed endpoint, a key, and a connection test before saving. `form.ts`
  holds its rules: what it checks, and which key a test or save uses. A
  stored key goes only to the base URL it was entered for, so changing the
  URL asks for the key again.
- `ModelPicker.tsx` lists what the endpoint serves, lets the user tick models
  or type one, suggests limits with `limits.ts`, and adds them.
- `ModelDialog.tsx` changes one model, its reasoning vocabulary included:
  endpoints disagree on the words `reasoning_effort` takes, so the model
  carries the list and `efforts.ts` holds the rules for editing and cycling
  through it. `ModelsPanel.tsx` is the whole list, the Models tab of the
  settings dialog.

The setup wizard (`features/setup`) uses the form and the picker for its
first provider.

Test it: `npm test -- providers` runs the limit, name, preset, form, and
reasoning-effort rules.
