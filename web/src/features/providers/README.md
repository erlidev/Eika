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
  or type one, suggests limits with `limits.ts`, and adds them. Image input
  starts ticked for a model the endpoint says reads images.
- `ModelDialog.tsx` changes one model, its reasoning vocabulary included:
  endpoints disagree on the words `reasoning_effort` takes, so the model
  carries the list and `efforts.ts` holds the rules for editing and cycling
  through it. It is also where image input is turned on, which lets messages
  to the model carry images; a test then sends one. `ModelsPanel.tsx` is the
  whole list, the Models tab of the settings dialog, with an Images badge on
  the models that take them.
- `EndpointDialog.tsx` shows what the harness's probe found about a provider's
  endpoint (backend, slots, per-request window, how its KV cache is divided)
  and lets the user set `backend`, `pool`, `slots` and the cache with its size.
  A value the user sets is kept when the endpoint is probed again, until "Use
  the probe's" hands it back. `endpoint.ts` holds the wording, the checks the
  harness repeats, and which fields a save sends. The provider card shows the
  one-line summary. Saving a provider with a new URL or key, and choosing
  its models, probe it again.

The setup wizard (`features/setup`) uses the form and the picker for its
first provider.

Test it: `npm test -- providers` runs the limit, name, preset, form,
endpoint, and reasoning-effort rules.
