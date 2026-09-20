# files

The open session's workspace as a file tree beside a Monaco editor.

- `FilesPanel.tsx` is the panel `app/panels.tsx` registers. It sits behind
  `RunningWorkspace`, saves on Ctrl/Cmd+S or the Save button, marks unsaved
  changes, asks before a file switch discards them, and shows a placeholder
  for a binary file or one over 2 MiB.
- `FileTree.tsx` lists a directory only when it is expanded, one query per
  directory, and follows the WAI-ARIA tree pattern with flat rows.
- `editing.ts` is the open file, its unsaved draft, and the expanded
  directories, per workspace, in a Zustand store so a tab switch keeps an
  edit. Its rules (`isDirty`, `decideOpen`, `afterSave`) are plain functions.
- `queries.ts` wraps the files, file, and save routes; a save refreshes the
  file, its directory, and the workspace diff.
- `CodeEditor.tsx` and `monaco.ts` are Monaco, loaded with `React.lazy` so it
  stays out of the main bundle. Its workers are bundled with `?worker`
  imports, and its theme is built from the design tokens.

Test it: `npm test -- files` covers the editing rules.
`npm run shot -- -s workbench --step "click Files"` shows the panel against
the mock harness.
