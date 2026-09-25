# Eika Style Guide

Rules for all code and documentation in Eika. They exist so that many agents
and humans can change the codebase over time and it still reads as one
system. Follow them exactly. If a rule is wrong for a case, change the rule
here in the same change, with a sentence of reasoning.

## 1. General

- **Correctness first.** A change is not done until `make check` passes and
  the behavior is covered by a test.
- **Small, whole changes.** One change does one thing completely: code,
  tests, docs. Do not leave TODOs for the parts you did not finish; either
  finish them or record them in `docs/PLAN.md`.
- **One way to do things.** Reuse the existing logger, config loader, HTTP
  client, error helpers, and UI primitives. Do not add parallel mechanisms.
- **Boring technology.** Standard library over third-party. Well-known
  third-party over novel. Every dependency must earn its place.
- **No speculative generality.** Do not add interfaces, options, or
  abstractions for needs that do not exist yet. Two concrete uses justify an
  abstraction; one does not.
- **Names carry meaning.** Names describe what a thing is or does in the
  domain (`Workspace`, `spawnSubagent`), never how it is implemented
  (`Manager`, `Helper`, `Util`, `Impl`, `Data`).

## 2. Go

### Layout and packages

- Module path `github.com/erlidev/eika` (change here if it changes).
- `cmd/<binary>/main.go` is thin: parse flags, load config, wire packages,
  run. All logic lives in `internal/`.
- Package names are short, lowercase, singular nouns: `tool`, `session`,
  `workspace`. No `utils`, `common`, `helpers`, `types`, `models`.
- One package per domain concept. A package exposes a small API and hides
  everything else. Prefer unexported by default.
- Dependencies point inward (see `AGENTS.md`). Never import `server` from
  anywhere; never import `workspace` from `tool`.
- Interfaces are defined where they are consumed, not where they are
  implemented, unless they are the package's main extension point
  (`tool.Tool`, `provider.Provider`, `search.Searcher`, `executor.Executor`).

### Code

- `gofmt` and `goimports` formatting; `go vet` and `staticcheck` clean.
- Errors: return them, wrap with context using `fmt.Errorf("verb noun: %w", err)`.
  Message is lowercase, no trailing punctuation, describes the operation
  that failed, not the fact that it failed ("read config", not "failed to
  read config"; the caller adds "failed"). Sentinel errors are
  `var ErrX = errors.New(...)`; typed errors are structs with an `Error()`
  method. Never `panic` outside `main` and `init` for programmer errors.
- Context: the first parameter of any function that does I/O, blocks, or
  can be cancelled is `ctx context.Context`. Never store a context in a
  struct.
- Concurrency: every goroutine has an owner that knows when it exits.
  Use `errgroup` or an explicit `sync.WaitGroup`. Channels are closed by the
  sender. Protect shared state with a mutex named `mu` placed directly above
  the fields it guards.
- Logging: `log/slog` only. Get the logger from the constructor argument or
  from `slog.Default()`; never a package-level logger variable. Log with
  structured keys, lowercase, snake_case: `slog.Info("workspace started",
  "workspace_id", id)`. Log at the boundary where an operation completes or
  fails, not at every step.
- Configuration is loaded once in `main` into a `config.Config` value and
  passed down. Packages never read environment variables directly.
- Constructors are `New(deps...) *T` or `NewT(deps...)`. Dependencies are
  explicit parameters, not globals.
- Options: prefer a plain struct parameter (`Options`) over functional
  options. Use functional options only when most fields are optional and
  the type is a public extension point.
- JSON: struct tags are `snake_case`. Wire types live in the package that
  owns them and are shared with the frontend via `docs/api/`.
- Time: `time.Time` in UTC everywhere. Durations are `time.Duration`, never
  bare integers.
- Do not use `init()` for registration. Registries are populated explicitly
  in one registry file per extension point.
- Generics: use them for containers and small algorithms. Do not use them to
  avoid defining a concrete type.

### Tests

- Standard `testing` package. No assertion libraries.
- Table-driven tests with `t.Run` and descriptive case names.
- Test the behavior through the package's public API. Internal tests
  (`package foo`) are for algorithms that are hard to reach otherwise.
- Fakes over mocks. `provider/providertest` ships a scripted fake provider;
  `executor/local` runs against a temp dir. Do not add a mocking framework.
- Tests that need Docker use the build tag `//go:build docker` and skip
  with a clear message when the socket is missing. `make check` runs them.
- A bug fix includes a test that fails without the fix. Name it after the
  behavior, not the bug number.
- Keep test files next to the code. Test helpers that are shared across
  packages live in a `<pkg>test` package.

### Comments and documentation in Go

- Every exported identifier has a doc comment that starts with its name and
  states what it is or does in one or two sentences. Say what, and why if
  the why is not obvious. Never say how; the code says how.
- Package doc comment in `doc.go` for every package under `internal/`:
  what the package is responsible for, what it depends on, and the main
  entry points. Three to ten lines.
- Inline comments only where the code cannot say it: a non-obvious
  invariant, a workaround with a link, a reason for an unusual choice. If
  you feel the need to comment what a block does, extract a named function.
- No commented-out code. No changelog comments. No author tags.

## 3. TypeScript and React

### Layout

```
web/src/
  app/          routes, layout shell, panel registry
  features/     one folder per domain feature (workspaces, sessions, terminal, editor, search)
  components/   shared presentational components; components/ui is shadcn
  api/          generated-or-mirrored wire types, client, WebSocket stream
  lib/          pure utilities with tests
```

- Feature folders own their components, hooks, and state. Cross-feature
  imports go through `features/<name>/index.ts` only.
- `components/ui/` is shadcn-managed. Do not hand-edit beyond what the
  shadcn CLI produces; wrap instead.

### Code

- `strict: true`, `noUncheckedIndexedAccess: true`. No `any`. Use `unknown`
  and narrow.
- ESLint and Prettier clean. Prettier settings are the repo's, not yours.
- Function components only, named exports, `PascalCase` files for
  components, `camelCase` for everything else.
- Props are a named `type XProps`. No `React.FC`.
- Server state: TanStack Query. Streaming and UI state: Zustand stores per
  feature. No Redux, no context for data.
- Wire types come from `api/` and mirror `docs/api/`. When the Go type
  changes, the TS type changes in the same commit.
- Tailwind for styling, shadcn for primitives. No CSS modules, no inline
  style objects except for computed values.
- Effects are a last resort. Derive state during render; subscribe with
  hooks that own their cleanup.
- Accessibility: every interactive element is a `button`, `a`, or input
  with a label. Keyboard navigation works for every panel.

### Web UI design

Eika's look is the "Dense dev tool" direction: a compact tool for people
who read code all day. Cool slate neutrals, one teal accent, small corners,
tight spacing, IBM Plex Sans for text and JetBrains Mono for anything a
machine produced or reads. `web/src/index.css` is the single source of the
look; a change to the look is a change to its tokens, never to a component.

- **Colour** comes only from the theme tokens: `background`, `foreground`,
  `card`, `popover`, `muted` / `muted-foreground` (secondary text, quiet
  surfaces), `accent` (hover and selected rows), `primary` (the one action
  on a screen, links, focus ring), `success`, `warning`, `destructive`,
  `border`. No Tailwind palette colours (`emerald-600`), no hex, rgb, or
  arbitrary colour values. Both themes follow from the tokens; do not add
  `dark:` colour overrides in components.
- **Type** uses the scale: `text-2xs` (badges, tiny metadata), `text-xs`
  (secondary labels, status bars), `text-sm` (the default for UI text),
  `text-base` (transcript prose, which is deliberately larger and looser than
  the chrome around it: it is read for minutes at a time), `text-lg` and up
  for headings only. No arbitrary sizes. Weights: 400 body, 500 labels and buttons, 600 headings.
- **Mono** (`font-mono`) for ids, paths, URLs, commands, image names, key
  hints, tool names, model ids, token counts, and code. Everything else is
  sans, including labels and what a person wrote (the composer, queued
  messages): a model's display name is sans, its id mono.
- **Spacing** uses the Tailwind scale, which the tokens make compact; do not
  add arbitrary padding or gaps. Rows in sidebar and panel trees are single
  line and truncate. A settings row truncates its name and its one-line
  details, and lets its actions wrap below the name when the name column
  would drop under `min-w-40`, so nothing is cut to a letter on a phone.
- **Shape**: `index.css` maps every radius step to `--radius`, the one
  small corner, so the `components/ui` primitives (whatever step they use)
  and feature code agree. Feature code writes `rounded-md` for any box
  (control, card, list, row, code chip) and `rounded-full` only for pills,
  dots, and switches. Borders separate regions; shadows only on floating
  layers (dialogs, popovers, a button floating over the transcript).
- **Components**: build from `components/ui` primitives and shared
  `components/` (`Notice` for inline status). A `Dialog` for a focused task,
  a panel for something that stays open beside the transcript, a `Card` only
  for a group of related settings.
- **Icons**: lucide, in three sizes that follow the host, as the button
  primitive does: `size-4` by default and in default controls, `size-3.5` in
  `sm` controls and beside `text-xs` (dense rows, status bars, `Notice`),
  `size-3` in `xs` controls (`h-6` buttons, `icon-xs` buttons) and beside
  `text-2xs`. An icon-only button has an `aria-label`.
- **Standalone screens** (sign-in, setup, load failure) use `Screen` from
  `components/Screen.tsx`: one bordered panel with no shadow on a muted
  backdrop, `max-w-sm` for a form or `wide` (`max-w-4xl`) beside a step
  list, `p-6 sm:p-8`, and `ScreenHeader` for the mark and a `text-xl`
  heading.
- **Loading**: `Splash` while a whole page loads; a pending `Notice`
  ("Loading the providers…") for a panel, tab, or dialog; a muted `text-xs`
  "Loading…" row, indented like the rows it stands for, in a sidebar or
  tree list.
- **Errors** sit beside the control that caused them and say which action
  failed and why: `ActionError` (or `failureText` in `lib/failure.ts` where
  a `Notice` does not fit) for a write, `LoadError` for a read, both reading
  "Could not <action>: <cause>". Never show a bare cause such as "name
  taken". The client (`api/client.ts`) words the cause; keep what it says
  accurate to the status, so a refusal never reads as "could not be reached".
- **Clickability**: anything that acts on a click says so before it is
  clicked. `index.css` states the pointer cursor once, for every element with
  a clickable role, so a component never sets `cursor-pointer` and never
  cancels it. A row, card header, tab, or tree item that responds to a click
  takes `hover:bg-accent` with `transition-colors`; `bg-accent` alone marks
  the selected one. A drag handle is at least ten pixels of target however
  thin the line it draws.
- **Scrollbars** are the thin themed ones `index.css` defines; a component
  neither restyles nor hides them.
- **Characters**: only what the bundled fonts cover. A symbol such as ⌘ or ←
  is a lucide icon, since a system font would draw it differently on every
  machine and break the visual baselines.

ESLint enforces the colour, type-size, radius, shadow, and icon-size rules
for `web/src`. `components/ui` is generated shadcn code, exempt from lint and
never hand-edited; its look changes only through the tokens.

### Tests

- Vitest for `lib/` and store logic. Component tests only for non-trivial
  interaction logic (queues, tree navigation, forms).
- End-to-end specs live in `web/e2e/` and run against the mock harness there;
  see `web/e2e/README.md`. A spec states its point with locator assertions:
  what is visible, enabled, focused, or described. Add a screenshot baseline
  only where pixels are the point (a theme, a phone layout, a rich component)
  and an ARIA snapshot where the point is what a screen says and offers. A
  baseline that only repeats a spec's assertions is churn, not coverage.
- A changed Go wire type means `make contract`, then the matching change in
  `web/src/api` and the mock harness; their tests say where.

## 4. Documentation

Documentation is for two readers: a human learning the system and an agent
about to change it. Both need the same thing: accurate, current, short.

- **Every package has a `doc.go`. Every feature folder has a README.md**
  of five to twenty lines: purpose, entry points, how to test it.
- **`docs/ARCHITECTURE.md`** describes the system as it is, not as planned.
  Diagrams are ASCII. Update it in the same change that changes structure.
- **`docs/EXTENDING.md`** has one section per extension point with a
  complete, copy-pasteable minimal example that compiles.
- **`docs/api/`** is the wire contract: one file for HTTP routes, one for
  WebSocket events. Each event and endpoint lists its fields with types and
  a one-line meaning.
- **`docs/PLAN.md`** holds decisions and phase status. Record decisions, not
  discussions.
- Write in plain declarative sentences. Present tense. Second person for
  instructions ("Register the tool in..."). No marketing language.
- Keep bug history out of docs. A fixed bug is a test, not a paragraph.
- Do not duplicate. Link to the one place a thing is explained.

## 5. Git

- Branch per unit of work. Commit messages: imperative subject under 60
  characters, blank line, body explaining why when the diff does not.
  Mention dependency additions with a reason.
- A commit passes `make check`. Do not commit generated files except the
  lockfiles, shadcn components, and `docs/api/contract.json`, which a test
  keeps in step with the Go types.
- Never commit secrets, tokens, or `.env` files. `.env.example` documents
  every compose variable.

## 6. Security

- Agent actions execute only through `executor` implementations backed by
  a sandbox. `executor/local` is `//go:build !prod` and test-only.
- The harness holds all credentials (LLM keys, git remote tokens), sealed
  with `internal/secret` before they reach the database and never returned
  by the API. Sandboxes receive a per-workspace `eikad` token and nothing
  else.
- All HTTP handlers require a bearer token except the health checks and the
  sign-in routes that hand one out.
- Validate and bound every input from an agent or the UI: path traversal in
  file tools, command length, output size, search query length.
- Never log secrets. Config values named `*_key`, `*_token`, `*_secret` are
  redacted by the config package's `String()`.

## 7. Checklist before finishing a change

1. `make check` passes.
2. New behavior has tests; fixed bugs have regression tests.
3. Exported identifiers have doc comments; new packages have `doc.go`.
4. `docs/` updated where affected (PLAN checklist, ARCHITECTURE, EXTENDING, api).
5. No new dependency without justification.
6. No TODOs left in code; unfinished work is recorded in `docs/PLAN.md`.
