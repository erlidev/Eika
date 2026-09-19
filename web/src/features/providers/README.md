# providers

The model providers and models agents run on. A provider is an
OpenAI-compatible endpoint and its key; a model is one of the models it
serves, with the limits and reasoning settings a run uses.

- `queries.ts` wraps `/api/providers` and `/api/models`, including the probe
  (which models does an endpoint serve) and the one-request model test. Every
  change invalidates the lists, the system counts, and the settings, which
  name the default model.
- `ProviderForm.tsx` connects a provider: a preset from `presets.ts` or a
  typed endpoint, a key, and a connection test before saving.
- `ModelPicker.tsx` lists what the endpoint serves, lets the user tick models
  or type one, suggests limits with `limits.ts`, and adds them.
- `ModelDialog.tsx` changes one model; `ModelsPanel.tsx` is the whole list,
  the Models tab of the settings dialog.

The setup wizard (`features/setup`) uses the form and the picker for its
first provider.

Test it: `npm test -- limits` runs the limit, name, and preset rules.
