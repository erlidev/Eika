# sandbox

What a workspace's container may consume, reach, and expose, and what it
consumes now.

- `SandboxPanel.tsx` is the Sandbox tab of a workspace session's context
  pane: CPU, memory, process, and network meters sampled every five seconds
  while the workspace runs; the hosts its egress was refused lately, each
  one click from the allowlist; its forwarded ports, each opening a preview
  in a new tab; and the editor, which applies a change at once without a
  restart.
- `SandboxFields.tsx` holds the fields: `LimitsFields` (sliders from no
  limit to the Docker host's capacity beside exact inputs), `EgressFields`
  (open, allowlist, or none, and the allowlist's chips), and `PortsFields`.
  The settings' Sandbox tab and the new workspace dialog use them too.
- `form.ts` is the editor's rules apart from React: a sandbox as field text
  (`SandboxDraft`), the harness's checks (`checkDraft`), allowlist entries
  (`normalizePattern`), and how a limit reads.
- `queries.ts` wraps `PUT /api/workspaces/{id}/sandbox`, the usage sample,
  and the preview link.

Test it: `form.test.ts` covers the rules; `e2e/specs/sandbox.spec.ts` drives
the panel, the settings tab, and the new workspace dialog against the mock
harness.
