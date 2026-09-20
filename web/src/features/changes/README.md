# changes

What the open session's workspace changed against the commit it started
from, and the actions that take the work somewhere.

- `ChangesPanel.tsx` is the panel `app/panels.tsx` registers. It sits behind
  `RunningWorkspace` and draws the diff one collapsible file at a time with
  the shared `components/DiffRows.tsx`, plus the untracked files only
  `git status` lists. It commits everything with a message (a 409 reads as
  "nothing to commit"), pushes to the hub, and pushes upstream; after an
  upstream push to a GitHub remote it links to the compare page
  (`lib/github.ts`) that opens a pull request.
- `queries.ts` wraps the diff, commit, and push routes. The diff refreshes on
  the `workspace.state` event (its query key sits under the workspace's),
  after a save in the Files panel, after a commit or push, and on Refresh.
- `lib/unifiedDiff.ts` parses `git diff` and `git status --porcelain`.

Test it: `npm test -- unifiedDiff github` covers the parsing and the link.
`npm run shot -- -s workbench --step "click Changes"` shows the panel against
the mock harness.
