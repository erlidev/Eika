# Refactor plan

A standing plan for cleaning up Eika without changing what it does. Agents
work through it over many sessions: pick the first unchecked item in the
lowest open phase, do it, tick it, and record anything learned in the log at
the bottom. Delete this file when every phase is done.

## Ground rules

These override convenience. They restate `AGENTS.md` and
`docs/STYLE_GUIDE.md` for this work; those two files win on any conflict.

- **No behavior change.** A refactor that changes a wire shape, an event, a
  log key, an error message a test or the UI reads, a default, or a pixel is
  not a refactor. If an item seems to need one, stop, leave it unchecked, and
  write the reason in the log.
- **The test suite is the oracle.** Before starting a session run
  `make check` on a clean tree and note the result. After every item, run it
  again; it must be exactly as green as before. Never edit a test to make a
  refactor pass, except to follow a renamed or moved symbol. Never run
  `make visual-update` or `make contract` as part of this plan: a changed
  baseline or `docs/api/contract.json` means behavior changed.
- **Docker tests count.** `make test-go` skips the handler, store, and
  workspace tests without a Docker daemon. Work on `internal/server`,
  `internal/store`, or `internal/workspace` only where Docker is reachable,
  and confirm the log does not say they skipped.
- **Smoke when required.** An item that touches a compose file, a
  Dockerfile, `deploy/`, or `server.Run` wiring also runs `make smoke`.
- **One item, one commit.** Subject under 60 characters, imperative
  (`Split server/mcp.go by resource`). Each commit passes `make check` on
  its own so any one can be reverted.
- **Coverage before surgery.** Before restructuring a file, check that its
  behavior is tested (`go test -cover ./internal/<pkg>`, or the vitest and
  e2e specs that exercise a component). If a path you are about to move has
  no test, add a characterization test first, in its own commit.
- **No new dependencies, no new mechanisms.** Tools for finding dead code
  run through `go run …@version` or `npx` and are never added to `go.mod` or
  `package.json`.
- **Prefer the smaller change.** When an item turns out larger than it
  looks, split it into sub-items here rather than landing a large diff.

## Baseline (2026-09-26)

Measured at `be89235` so progress can be compared.

| Area                        | Size                                         |
|-----------------------------|----------------------------------------------|
| Go, non-test                | 36.7k lines                                  |
| Go, tests                   | 23.6k lines                                  |
| TypeScript, `web/src` (no `components/ui`) | 29.8k lines                   |
| `internal/server`           | 9.8k lines in 28 files; `mcp.go` is 1,367    |
| Largest Go files            | `server/mcp.go` 1367, `mcp/pool.go` 957, `server/providers.go` 789, `server/runs.go` 762, `agent/agent.go` 743 |
| Largest TS files            | `e2e/harness/mock.ts` 2190, `api/types.ts` 1163, `context/ContextInspector.tsx` 1036, `mcp/MCPServerView.tsx` 970 |
| Docs                        | 4.2k lines; `api/http.md` 1472, `EXTENDING.md` 767, `ARCHITECTURE.md` 576 |
| `doc.go` over 10 lines      | 23 of the packages (rule: 3 to 10)           |
| Feature READMEs over 20 lines | 13 of 16 (rule: 5 to 20); `session` is 98 |
| Cross-feature imports bypassing `index.ts` | 11, listed in phase 3     |
| TODO / FIXME in code        | none                                         |

The codebase is already disciplined: comments mostly explain why, not how,
and lint is clean. The work is trimming, splitting, and enforcing the rules
that exist, not rewriting.

## Phase 0: Tooling and baseline

Make the gate trustworthy and gather the lists later phases work from.

- [x] Run `make check` on a clean tree; record time and result in the log.
- [x] Run `make test-go` with Docker reachable; confirm no package skipped.
- [x] Record per-package coverage: `go test -tags docker -cover ./cmd/... ./internal/...`.
      Paste the table in the log. Packages under 60% get characterization
      tests before phase 2 touches them.
- [x] Generate the Go dead-code list:
      `go run golang.org/x/tools/cmd/deadcode@latest -test ./cmd/...` and
      `go tool staticcheck -checks U1000 -tags docker ./cmd/... ./internal/...`.
      Paste results in the log as phase 1 input.
- [x] Generate the TS dead-code list: `cd web && npx knip --no-exit-code`
      (no install; drop any config it writes). Paste unused files, exports,
      and dependencies in the log.
- [x] Check whether `golangci-lint` is installed where `make check` runs. If
      it is not, the `revive`, `gocritic`, and `errorlint` rules in
      `.golangci.yml` never run; note this, and run it once via
      `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./cmd/... ./internal/...`
      to collect findings for phase 2.
- [x] Fix the stale header of `.golangci.yml`: it mentions CI, and
      `AGENTS.md` says there is none.
- [x] Run `go mod tidy`. `github.com/jackc/pgx/v5` is a direct dependency
      sitting in an indirect `require` block; tidy regroups it. Commit only
      if `go.mod`/`go.sum` change and `make check` passes.

## Phase 1: Remove dead and redundant code

Delete what nothing uses. Each deletion is proven dead by the tool output
from phase 0 plus a grep, including `web/e2e`, `docs/`, and tests.

### Go
- [x] Delete unused functions, types, constants, and fields from the
      deadcode and U1000 lists. Exported identifiers used only by tests of
      the same package are candidates too; keep ones a `*test` helper
      package or another package's test uses.
- [x] Consolidate the HTTP JSON helpers. `writeJSON`/`writeError` exist in
      both `internal/server/json.go` and `internal/eikad/daemon.go`. They may
      stay separate (the packages are separate binaries and must not share
      an import that drags `server` in), but their behavior should match;
      diff them and align or document the difference in one line.
- [x] Review the `http.Client` constructions (`executor/sandbox`,
      `server/mcp.go:1358`, `server/wire.go`, `workspace/host.go`,
      `search/fetch/client.go`). Where two build the same client, reuse one.
      Do not change timeouts.
- [x] Look for parallel mechanisms the style guide forbids: a second config
      read (`os.Getenv` outside `cmd/` and `config`), a package-level
      logger, `init()` registration, `panic` outside `main`/`init`.
      `git grep -nE 'os\.Getenv|slog\.New\(|func init\(|panic\(' internal cmd`.
- [x] Decide on compatibility shims such as the "is no longer read from the
      file" error in `internal/config/config.go`. Removing one changes
      behavior for old deployments, so do not remove it here; list each shim
      in the log for the owner to decide.

### Frontend
- [ ] Delete unused files and exports from the knip list. Re-run
      `npm run typecheck` and `npm test` after each feature folder.
- [ ] Delete unused `package.json` dependencies knip reports, after a grep
      over `web/src`, `web/e2e`, `index.css`, and the Vite and ESLint configs
      (`shadcn` and `tw-animate-css` are imported from `index.css`, not TS).
- [ ] `web/src/lib/utils.ts` only re-exports `cn` from the `cn` package,
      and `components/ui` imports `cn` directly. Leave `components/ui` alone
      (shadcn-generated); make the rest of `web/src` pick one import path
      and use it everywhere.
- [ ] Remove the two `eslint-disable` comments in `web/e2e/fixtures.ts` and
      the one in `features/session/Transcript.tsx` if the code can satisfy
      the rule without them; otherwise leave a one-line reason on each.

### Test harness
- [ ] `web/e2e/harness/mock.ts` (2190 lines) mirrors the Go API. Find
      handlers for routes the UI no longer calls (compare with
      `web/src/api/routes.ts`) and delete them.

## Phase 2: Structure and code practice

Split the files that have grown past one responsibility and enforce the
style guide's code rules. Pure moves first, edits after, in separate commits,
so a reviewer can see a move changed nothing.

### Go: split large files
Split by resource or concern within the same package. No new packages
without a `docs/DECISIONS.md` entry.

- [ ] `internal/server/mcp.go` (1367): wire types, validation, handlers,
      and OAuth routes into separate files (for example `mcp_wire.go`,
      `mcp_auth.go`).
- [ ] `internal/mcp/pool.go` (957): connection lifecycle vs. per-workspace
      bookkeeping.
- [ ] `internal/server/providers.go` (789) and `internal/server/runs.go`
      (762): wire types vs. handlers.
- [ ] `internal/agent/agent.go` (743): move the turn-restoration and
      response-assembly helpers into their own file.
- [ ] `internal/mcp/mcptest/server.go` (755): only if it helps readers; it
      is test support.
- [ ] After the splits, check each file in `internal/server` has one
      subject and its name says which.

### Go: practice
- [ ] Error strings: `git grep -nE 'Errorf\("(failed|unable|could not|error)' internal`
      and fix to the "verb noun: %w" form. Leave strings a test asserts or
      the UI shows as is, and list them in the log.
- [ ] `%v` wrapping errors where `%w` belongs: errorlint findings.
- [ ] Context: functions doing I/O without `ctx` first; contexts stored in
      structs outside the owned-goroutine exception.
- [ ] Goroutines without an owner (`git grep -n 'go func' internal`): each
      has a `WaitGroup`, `errgroup`, or a documented `Close`.
- [ ] Mutexes named `mu` sitting above the fields they guard.
- [ ] Log keys in `snake_case`; logging at boundaries, not every step.
- [ ] Names: no `Manager`, `Helper`, `Util`, `Impl`, `Data` in type or
      function names (`git grep -nE 'type \w*(Manager|Helper|Util|Impl|Data)\b'`).
- [ ] Interfaces declared at the consumer, except the named extension
      points. Remove interfaces with one implementation and no test fake.
- [ ] Apply the golangci-lint findings from phase 0 that are real; do not
      add `//nolint`.

### Frontend: split large components
- [ ] `features/context/ContextInspector.tsx` (1036): extract subcomponents
      into sibling files; keep the exported API.
- [ ] `features/mcp/MCPServerView.tsx` (970): same.
- [ ] `app/Sidebar.tsx` (670): same.
- [ ] `features/settings/SearchSettings.tsx` (553) and
      `features/profiles/SettingsEditor.tsx` (525): same.
- [ ] `api/types.ts` (1163): split by domain (`types/mcp.ts`, …) only if
      `api/index` keeps every import path working; otherwise leave it.
- [ ] `e2e/harness/mock.ts` (2190): split handlers by resource like the Go
      server files.

### Frontend: practice
- [ ] Inline `style={{…}}` objects: keep only computed values (widths,
      transforms). Files: `app/Sidebar.tsx`, `components/ResizableSplit.tsx`,
      `context/ContextInspector.tsx`, `context/parts.tsx`,
      `profiles/SettingsEditor.tsx`, `sandbox/SandboxPanel.tsx`,
      `session/ContextMeter.tsx`, `session/SessionTreePanel.tsx`.
- [ ] `useEffect` audit: `git grep -n useEffect web/src`. Replace effects
      that derive state with render-time derivation.
- [ ] Props typed as named `type XProps`, no `React.FC`, named exports only.
- [ ] Server state in TanStack Query, UI state in feature Zustand stores;
      no React context carrying data.

## Phase 3: Boundaries

Make dependency rules hold everywhere, then make them checked.

- [ ] Verify Go import direction: nothing imports `internal/server`; `tool`
      never imports `workspace`.
      `go list -deps -f '{{.ImportPath}}' ./internal/... | …` or
      `git grep -n '"github.com/erlidev/eika/internal/server"' -- ':!internal/server'`.
- [ ] Add a Go test (in the style of `contract_test.go`) that fails when a
      forbidden import appears, so the rule stops depending on review.
- [ ] Route these cross-feature imports through the target feature's
      `index.ts` (export what is needed there):
  - [ ] `features/session/ToolsPanel.tsx` → `profiles` (4 imports)
  - [ ] `features/session/RunStatusBar.tsx` → `providers`
  - [ ] `features/profiles/ToolPicker.tsx` → `session`
  - [ ] `features/profiles/SettingsEditor.tsx` → `session`
  - [ ] `features/profiles/store.ts` → `settings`
  - [ ] `features/profiles/ProfilesSettings.tsx` → `settings`
  - [ ] `features/context/segments.ts`, `parts.tsx`,
        `ContextInspector.tsx` → `profiles`
- [ ] Enforce it in `web/eslint.config.js` with `no-restricted-imports`
      (built into ESLint, no new dependency): a file under
      `features/<a>/` may not import `@/features/<b>/<anything but index>`.
- [ ] Circular imports between features that the index routing exposes:
      break them by moving the shared piece to `lib/` or `api/`.

## Phase 4: Comments

The rule: a comment says what the code cannot. Exported identifiers keep a
doc comment. Delete, don't reword, when in doubt.

- [ ] Go, per package, starting with the densest (`provider`, `executor`,
      `event`, `config`, `session`): delete inline comments that restate
      the next line, narrate a block (extract a function instead), or tell
      bug history ("used to", "previously", "no longer" where it describes
      the past rather than current behavior).
- [ ] Go doc comments: each starts with the identifier's name and is one or
      two sentences. Trim longer ones; move design reasoning that matters
      to `docs/ARCHITECTURE.md` or `docs/DECISIONS.md` if it is not already
      there.
- [ ] Struct field comments on wire types (`server/*` bodies): keep ones
      that give meaning or allowed values; they feed `docs/api/`. Delete
      ones that repeat the field name.
- [ ] TypeScript, per feature, starting with the densest (`sessions`,
      `api`, `components`, `terminal`, `search`): same rules. JSDoc on
      exported functions only where the name and types do not say it.
- [ ] Remove any commented-out code (`git grep -nE '^\s*//\s*(if|for|return|func|const|let|import)\b'`).

## Phase 5: Documentation

Shorten to the rules in the style guide, remove duplication, and make the
docs match the code after phases 1 to 4.

- [ ] Trim `doc.go` to three to ten lines in the 23 packages over the limit,
      largest first: `server` (43), `mcp` (25), `workspace` (23), `agent`
      (23), `session` (22), `provider/openai` (20), `search` (18), `event`
      (18), `subagent`, `store`, `egress` (17), then the rest. Detail that
      is not elsewhere moves to `ARCHITECTURE.md`.
- [ ] Trim feature READMEs to five to twenty lines: `session` (98) first,
      then `settings` (34), `context`, `profiles` (29), `providers` (28),
      `sessions` (26), `connect`, `sandbox` (24), and the rest over 20.
- [ ] `README.md`: remove what `AGENTS.md` or `docs/` already says and link
      instead. Decide whether "Upgrading from an environment-configured
      version" is still needed (it is history); record the decision.
- [ ] `docs/ARCHITECTURE.md`: update for every file split and moved
      responsibility in phases 2 and 3; cut paragraphs that repeat a
      `doc.go` or `DECISIONS.md`.
- [ ] `docs/DECISIONS.md`: one entry per decision, choice plus reason plus
      what was rejected; remove discussion and history. Add an entry for
      the boundary tests from phase 3.
- [ ] `docs/EXTENDING.md`: confirm every example still compiles against
      the code (paste each into a scratch package and `go build`), and that
      the registry table in `AGENTS.md` matches.
- [ ] `docs/api/http.md` (1472): every route in `routes.go` appears once,
      and nothing is listed that the router does not serve. Remove prose
      that repeats `contract.json` field-by-field where the contract is the
      source.
- [ ] `docs/api/events.md` against `internal/event` and `web/src/api/events.ts`.
- [ ] `web/e2e/README.md` against the `shot` CLI's `--help`.
- [ ] Makefile `##` help comments: accurate and short.

## Phase 6: Final pass

- [ ] Re-measure the baseline table and add an "after" column.
- [ ] Run `make check` and `make smoke` on the final tree.
- [ ] Re-run the phase 0 dead-code tools; the lists are empty or every
      remaining entry is explained in the log.
- [ ] Confirm `contract.json` and every visual baseline are byte-identical
      to the baseline commit: `git diff be89235 -- docs/api/contract.json web/e2e/__screenshots__ web/e2e/__aria__`
      shows nothing.
- [ ] Move lasting lessons to `STYLE_GUIDE.md` or `DECISIONS.md`, then
      delete this file.

## Log

Newest first. One entry per session: date, items done (commit hashes),
`make check` result, anything skipped and why, and findings for later items.

- 2026-09-26, phase 1 (Go). `make check` green before each commit.
  - Dead code: deleted `mcptest.Image` (`1598c49`); moved
    `agent.MemoryStore` into the agent tests as `memoryStore`, the only
    users. Kept `agent.NewSession` (server tests use it),
    `provider.AssistantMessage` (openai tests), and `executor.Rel`
    (`executor/local`, itself test-only).
  - JSON helpers: `writeJSON` matches except that `server` skips the body
    for an untyped `nil`, which eikad never passes. The error bodies differ
    on purpose (eikad's flat `ErrorResponse` is private to
    `executor/sandbox`); documented both in `eikad/daemon.go`.
  - `http.Client`: no change. The clients without a timeout
    (`executor/sandbox`'s default, `server/mcp.go`) have a nil `Transport`,
    so they already share `http.DefaultTransport` and its pool. The rest
    differ on purpose: `wire.go` has `search.Timeout`, `workspace/host.go`
    5 s, `search/fetch` and `egress` a guarded dialer. `mcp.NewPool`
    defaults to `http.DefaultClient`, equivalent to what `server` passes.
  - Parallel mechanisms: none. Env reads outside `cmd/` and `config` are in
    test helpers (`storetest`) or pass the sandbox's own environment to a
    child (`eikad` `SHELL` and `os.Environ`, `executor/local`, the hub's
    git). No package-level logger or `init()`. `panic` only in test
    helpers (`mcptest`, `providertest`). Loggers default to `slog.Default()`
    (allowed) except `search.Engine`, which defaults to discard; aligning
    it would change logging, so it stays.
  - Compatibility shims, for the owner to decide:
    1. `config.movedToUI` / `checkMovedKeys`: a config file setting
       `models` or `subagents` fails with a pointer to the UI.
    2. README "Upgrading from an environment-configured version"
       (`deploy/eika.yaml`, `deploy/.env`, the `eika-local` project).
       Phase 5 has an item for it.
    3. `deploy/.env` in `.gitignore` and `.dockerignore`: keeps an old
       secrets file out of git and images. Cheap; keep while any old
       checkout may have one.
    The MCP "legacy" transports are protocol support, not shims.
- 2026-09-26, phase 0 (all but the `.golangci.yml` item, which has its own
  commit). Findings below are the input for phases 1 and 2.
  - `make check` on a clean tree: green in 91 s. 35 vitest files, 386
    tests; 127 visual tests; golangci-lint "not installed, skipping".
  - `go test -tags docker -v` with Docker 29.8.1: no `--- SKIP` anywhere;
    server, store, and workspace ran (26 s, 12 s, 17 s).
  - Coverage (`-tags docker`, statements): agent 94.8, config 94.4,
    contextfile 94.1, egress 86.5, eikad 84.7, event 86.2, executor 93.8,
    executor/local 86.7, executor/sandbox 86.2, mcp 74.0, mcp/oauth 73.3,
    netguard 84.2, provider 61.9, provider/openai 91.8, search 82.8,
    search/arxiv 94.1, search/fetch 84.1, search/filter 90.6,
    search/github 96.8, search/page 100, search/web 87.6,
    search/wikipedia 83.3, secret 77.6, server 80.0, server/servertest
    70.5, session 84.2, store 78.9, subagent 40.5, tool 92.3,
    tool/builtin 86.6, utility 100, workspace 74.2, workspace/hub 85.0.
    No tests: `cmd/eika`, `cmd/eikad`, and the `*test` helper packages.
    **Under 60%: `subagent` only**; it needs characterization tests
    before phase 2 touches it. `provider` (61.9) is close.
  - `deadcode -test ./cmd/...` (reachable from the binaries only):
    `agent.NewSession`, `agent.NewMemoryStore`, `MemoryStore.Append`,
    `MemoryStore.Messages` (agent/conversation.go), `executor.Rel`,
    `provider.AssistantMessage`. All are used by tests, several across
    packages (`AssistantMessage` by `provider/openai` tests, `executor.Rel`
    by `executor/local`), so phase 1 keeps them unless a move to test code
    is clearly better. Note `internal/executor/local` itself is imported
    only by tests.
  - `deadcode -test ./cmd/... ./internal/...`: only `mcptest.Image`
    (mcp/mcptest/server.go:748) is unused anywhere. Without `-tags docker`
    it also lists `storetest`, which only docker-tagged tests use.
  - `staticcheck -checks U1000 -tags docker`: nothing.
  - knip (no config written, nothing installed in the project):
    - Unused dependencies: none reported.
    - Unused files: `e2e/smoke/model.ts` and `playwright.smoke.config.ts`
      are false positives (`make smoke` uses them); `components/ui/card`,
      `scroll-area`, `tooltip` are shadcn files nothing imports.
    - Unused exports outside `components/ui`: e2e `defaultViewport`
      (driver.ts), `wrongPassword` (mock.ts), `builtinPrompts`,
      `estimate`, `toolSchemaOf`, `defaultProfileOf` (profiles.ts),
      `defaultAllowlist` (world.ts); `api/connection` `isConnected`,
      `apiUrl`; `api/routes` `getProject`, `getSessionPath`; `api/stream`
      `globalTopic`; `app/panels` `panels`; `context/queries`
      `useNextRequest`, `useRecordedRequests`, `useRecordedRequest`;
      `mcp/form` `urlProblem`; `session/renderers/registry` `args`,
      `renderers.tsx` `toolRenderers`; `lib/persisted`
      `readPersistedNumber`. Index re-exports nobody imports: `profiles`
      `useProfiles`; `projects` `useCreateProject`, `useUpdateProject`;
      `providers` `useCreateModel`, `useCreateProvider`, `useDeleteModel`,
      `useDeleteProvider`, `useTestModel`, `useUpdateProvider`; `sandbox`
      `sameSandbox`; `session` `useSessionOutline`, `useRunStatus`,
      `useTools`, `toolSummary`, `rendererFor`, `toolRenderers`;
      `settings` `settingNumber`, `settingString`; `workspaces`
      `useCreateWorkspace`. Several may be needed again by phase 3's index
      routing; check before deleting.
    - Unused exports inside `components/ui` (shadcn, leave alone):
      alert-dialog Media/Overlay/Portal/Trigger, `badgeVariants`,
      `buttonVariants`, command Shortcut/Separator, dialog
      Close/Overlay/Portal/Trigger, input-group Button/Text/Input/Textarea,
      select ScrollUp/DownButton, `tabsListVariants`, `Toggle`.
    - Unused exported types (55): `ScenarioName` (e2e/scenarios.ts); the
      event payload types in `api/events.ts` (TurnStart, MessageDelta,
      ReasoningDelta, MessageReset, ToolCall, ToolOutput, TurnEnd,
      RunError, QuestionAsked, SubagentStarted, SubagentFinished,
      WorkspaceState, MCPServerChanged, MCPElicitation, SessionTitle);
      in `api/types.ts` WorkspaceLifecycle, SessionKind, Role,
      MessageToolCall, MessageMetrics, RunState, SettingsDefaults,
      SearchUsage, MCPImplementation, MCPCapabilities, MCPResource,
      MCPResourceTemplate, MCPPromptArgument, MCPLogLine, MCPChallenge,
      MCPAuth, ElicitationMode, ElicitationAction, RequestParameters;
      PanelProps, NoticeTone, SplitSide, SectionTab, ChoiceOption,
      EditorKind, PortDraft, SandboxProblems (sandbox index), Option and
      FieldKind (session/elicitation), ToolRenderer and ToolRendererProps
      (session index), UserItem, ErrorItem, NoticeItem, ToolResultRecord
      (session/transcript), SandboxDefaults and SettingsTab (settings
      index), WorkspaceAction (workspaces index), DiffHunk. The API types
      mirror the wire protocol and are used inside their own file; only
      the `export` keyword is unused. Decide in phase 1 whether dropping
      `export` is worth it (probably not for `api/`).
  - golangci-lint is **not installed** here, so revive, gocritic,
    errorlint, bodyclose, misspell, and unconvert never ran in
    `make check`. One run of v2 (latest) found 100 issues:
    - errcheck 53 (28 in tests): almost all `defer x.Close()` and
      `io.Copy` in proxies. The config does not enable v2's
      `std-error-handling` exclusion preset; phase 2 should decide between
      that and explicit handling rather than fix each.
    - bodyclose 26 (22 in tests); non-test: executor/sandbox/sandbox.go
      234 and 348, mcp/http.go 370, mcp/legacysse.go 58. Check each: the
      body may be closed by a callee.
    - errorlint 2: mcp/elicit.go:128, store/storetest/storetest.go:117.
    - gocritic 3: offBy1 search/fetch/markdown.go:419 and
      search/page/page_test.go:52; ifElseChain server/workspaces.go:291.
    - revive 12: builtin shadowing (`clear` config_test.go:187, `max`
      eikad/files.go:28, `real` eikad/path.go:24 and executor/local/local.go
      36/210/226, `close` search/fetch/markdown.go:414, `comparable`
      search/page/page.go:94); unused params egress_test.go 117/158,
      server/previews.go:172; context-as-argument
      provider/openai/openai_test.go:63.
    - staticcheck quickfix 2: egress/proxy.go:222 (QF1008),
      mcp/headers.go:16 (QF1001).
    - unconvert 1: provider/openai/openai.go:217.
    - gofmt 1: agent/agent_test.go:1208 (passes `gofmt -l`, so this is
      golangci's `-s` simplify).
  - `go mod tidy`: no change, so no commit. Tidy does not move an entry
    between `require` blocks; `jackc/pgx/v5` stays in the indirect block
    without `// indirect`. Moving it is a manual one-line edit if wanted.
- 2026-09-26: plan written at `be89235`. No items started.
