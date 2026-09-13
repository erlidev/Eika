# projects

Projects are the git repositories Eika knows. This feature lists them, creates
them, and deletes them.

- `queries.ts` wraps `/api/projects`. Deleting a project also invalidates the
  workspace and session lists, because the harness deletes those with it.
- `CreateProjectDialog.tsx` is the form. A remote project names the
  environment variables holding its credentials; it never carries their values.
- `validate.ts` mirrors the rejections the harness makes, so the common
  mistakes are caught before a round trip. The harness stays the authority.

The list itself is drawn by `app/Sidebar.tsx`, which composes projects,
workspaces, and sessions into one tree.

Test it: `npm test -- validate` runs the form's rules.
