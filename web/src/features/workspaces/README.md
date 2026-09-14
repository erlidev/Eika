# workspaces

A workspace is one sandbox container holding a checkout of a project. This
feature creates them, starts and stops them, destroys them, and shows their
lifecycle state.

- `queries.ts` wraps `/api/workspaces`. `useWorkspaces(projectId?)` lists, and
  `useWorkspace(id)` reads the one a session belongs to.
- `useWorkspaceEvents.ts` subscribes to `workspace:<id>` and invalidates the
  cached workspace when the harness reports a new state. Nothing polls.
- `WorkspaceStateBadge.tsx` is that state as a badge.
- `CreateWorkspaceDialog.tsx` creates one, optionally on a branch or an image
  other than the deployment's default.

Test it: no unit test; the behaviour is queries over the API. Exercise it
against a live harness with `make dev` and `make sandbox`.
