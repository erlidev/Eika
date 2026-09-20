# projects

Projects are the git repositories Eika knows. This feature lists them, creates
them, changes their settings, and deletes them.

- `queries.ts` wraps `/api/projects`. Deleting a project also invalidates the
  workspace and session lists, because the harness deletes those with it.
- `ProjectForm.tsx` is the create form, used by `CreateProjectDialog.tsx` and
  the setup wizard. A private remote takes a username and a password or
  token; the harness encrypts the password and never returns it.
- `ProjectSettingsDialog.tsx` changes the default branch and a remote's
  credentials. The harness fetches with new credentials before it keeps them.
- `validate.ts` mirrors the rejections the harness makes, so the common
  mistakes are caught before a round trip. The harness stays the authority.

The list itself is drawn by `app/Sidebar.tsx`, which composes projects,
workspaces, and sessions into one tree.

Test it: `npm test -- validate` runs the form's rules.
