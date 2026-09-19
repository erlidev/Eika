# setup

The guided setup a new harness opens with, so that running Eika is
`docker compose up` and a browser.

- `SetupWizard.tsx` walks the steps in `steps.ts`: choose the sign-in
  password, connect a provider (`features/providers/ProviderForm`), pick its
  models (`ModelPicker`), check the sandbox and choose the default model
  (`features/settings`), and add a first project (`features/projects`).
- `steps.ts` holds the order and `firstStep`, which resumes a setup left
  halfway at the first thing still missing.

The app shows the wizard until the harness has a password and the
`setup_complete` setting is true. Finishing, or skipping the rest, writes it;
Settings, General can set it back to run the setup again.

Test it: `npm test -- steps` runs the order and the resume rule.
