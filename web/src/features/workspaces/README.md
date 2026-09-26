# workspaces

A workspace is one sandbox container holding a checkout of a project. This
feature creates them, starts and stops them, destroys them, and shows their
lifecycle state.

- `queries.ts` wraps `/api/workspaces`. `useWorkspaces(projectId?)` lists, and
  `useWorkspace(id)` reads the one a session belongs to.
- `useWorkspaceEvents.ts` subscribes to `workspace:<id>` and invalidates the
  cached workspace when the harness reports a new state. Nothing polls.
- `WorkspaceStateBadge.tsx` is that state as a badge.
- `RunningWorkspace.tsx` gates a panel that works inside the sandbox (Files,
  Terminal, Changes): its content while the workspace runs, its state and a
  Start button otherwise.
- `CreateWorkspaceDialog.tsx` creates one, optionally on a branch or an image
  other than the deployment's default. Its Sandbox section, folded by
  default, gives the workspace limits, a network, and ports of its own with
  the fields from `features/sandbox`; folded, it sends none and the harness
  applies the settings' defaults.

Test it: no unit test; the behaviour is queries over the API. Exercise it
against a live harness with `make dev` and `make sandbox`.
