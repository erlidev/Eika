# terminal

A shell inside the open session's workspace, drawn by xterm.js.

- `TerminalPanel.tsx` is the panel `app/panels.tsx` registers. It sits behind
  `RunningWorkspace`, fits the terminal to the pane with a `ResizeObserver`,
  and offers Restart after the shell exits and Reconnect after the connection
  drops. It loads `ShellView.tsx`, and with it xterm, lazily.
- `session.ts` holds the one live shell: the xterm instance, its fit addon,
  and the `TerminalSocket` from `api/terminal.ts`. It outlives the panel so a
  tab switch keeps the shell and its screen; opening the panel for another
  workspace ends it. Colours follow the theme through `lib/themeColor.ts`.

Test it: `npm test -- terminal` covers the socket's frames and base64 byte
handling. `npm run shot -- -s workbench --step "click Terminal"` shows the
panel against the mock harness, whose terminal echoes what is typed.
