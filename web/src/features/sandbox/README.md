# sandbox

The limits and network a sandbox is created with, as the Computer tab of the
settings edits them.

- `SandboxFields.tsx` holds the fields: `LimitsFields` (sliders from no
  limit to the Docker host's capacity beside exact inputs) and
  `EgressFields` (open, allowlist, or none, and the allowlist's chips).
  `features/settings/ComputerSettings.tsx` uses them.
- `form.ts` is the editor's rules apart from React: the limits and egress as
  field text (`SandboxDraft`), the harness's checks (`checkDraft`), allowlist
  entries (`normalizePattern`), and how a limit reads.

Test it: `form.test.ts` covers the rules; `e2e/specs/computer.spec.ts` drives
the tab against the mock harness.
