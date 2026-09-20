# Visual testing

Drive the UI in a real browser against a **mock harness** and screenshot the
result. No Go server, Postgres, or Docker needed: `harness/mock.ts` answers
every `/api` route and the `/api/events` WebSocket from an in-memory world,
and plays scripted agent replies so a sent message streams like a real run.
A workspace's terminal socket is a shell that echoes what is typed (`exit`
ends it), and its files come from the world's `files`.

Chromium is needed once: `npx playwright install chromium`. `make visual`
installs it when it is missing.

## Look at the UI (`shot`)

```sh
npm run shot -- --list                                  # scenarios and their URLs
npm run shot -- -s workbench                            # screenshot a scenario
npm run shot -- -s workbench --step "click Settings"    # act, then screenshot
npm run shot -- -s agent-tools --step "send Run the tests" --step "shot after-run" \
                --step "click go test ./internal/webhook/..." --theme dark
npm run shot -- --help                                  # every option and step
```

Each run starts its own Vite server (about two seconds) and prints JSON:

```json
{
  "ok": true,
  "shots": [".../e2e/out/final.png"],
  "aria": ".../e2e/out/final.aria.yml",
  "consoleErrors": [],
  "pageErrors": [],
  "unhandledApi": [],
  "url": "..."
}
```

- Read the PNGs to see the page. `final.png` is taken after the last step.
- Read `final.aria.yml` to learn what a target can be called: it lists every
  button, tab, field, and heading by role and name.
- On a failed step `ok` is false, `failedStep` and `error` say why, and
  `failed.png` with `failed.aria.yml` show the page where it stopped.
- `unhandledApi` lists routes the UI called that the mock does not serve yet;
  add them to `buildRoutes` in `harness/mock.ts`.
- `--out <dir>` keeps runs apart; `--url http://localhost:5173` reuses a
  running `npm run dev` instead of starting a server.
- `--fail "GET /api/providers = 500"` makes a route fail from the first
  request, to see a load error; repeat it for several routes.

### Steps

One action per `--step`, or one per line in a `--steps <file>`:

| Step                                           | Does                                                             |
| ---------------------------------------------- | ---------------------------------------------------------------- |
| `click <target>` / `hover <target>`            | Click or hover it.                                               |
| `scroll <target>`                              | Bring it into view, such as the end of a long dialog.            |
| `fill <target> = <value>`                      | Replace a field's text.                                          |
| `select <target> = <option>`                   | Pick from a native or Radix select.                              |
| `press <key>` / `type <text>`                  | Keyboard: `Enter`, `Escape`, `Control+K`; text into the focus.   |
| `send <message>`                               | Type into the session composer and send. The mock agent replies. |
| `wait <ms or target>`                          | Pause, or wait until something is visible.                       |
| `goto <path>` / `reload`                       | Navigate.                                                        |
| `theme light\|dark` / `viewport 390x844`       | Change how the page renders.                                     |
| `emit <event json>`                            | Push an event from docs/api/events.md onto the stream.           |
| `fail <route> = <failure>` / `heal <route>`    | Make a route fail until healed; see "Failing routes" below.      |
| `shot <name> [= <target>]` / `fullshot <name>` | Screenshot the viewport, one element, or the full page.          |
| `aria`                                         | Save the accessibility tree as `page.aria.yml`.                  |

A **target** is what the UI calls something: `Settings`, `New project`,
`Password`, `30s`. It is tried as a button, link, tab, menu item, option,
tree item, checkbox, switch, radio, and combobox name, then a field label, a
placeholder, and exact text, in that order; while a modal dialog is open,
labels and text behind it do not count. A target that never appears fails
after 10 seconds. Use a Playwright selector when a name is
ambiguous: `role=dialog`, `role=button[name="Delete gpt-5"]`, `text=/rate limit/`,
`label=Password`, `css=.foo`.

Every step waits for the page to settle (the mock idle, a scripted reply
finished, fonts loaded, animations done), so no `wait` is needed after one.
The clock is fixed at `2026-03-14T15:00Z` so relative times never change.

### Failing routes

A route is named as `docs/api/http.md` writes it, with `{id}` for a path
parameter: `GET /api/models`, `PATCH /api/providers/{id}`. A failure is
one of:

| Failure    | The page sees                                                                  |
| ---------- | ------------------------------------------------------------------------------ |
| `500`      | That status with the documented error body; `409 name taken` sets the message. |
| `502 bare` | That status with no body, as a proxy in front of a stopped harness answers.    |
| `network`  | No answer at all: the harness is unreachable.                                  |
| `hang`     | A request that never ends, to see a loading state.                             |

A failed route stays failed until `heal`, so a test can show the error, heal
the route, and press Retry. In a spec, pass `failing: { "GET /api/models":
{ status: 500 } }` to `open`, or call `eika.mock.failRoute` and
`eika.mock.healRoute`. An unknown route throws and lists the known ones.

## Scenarios

`harness/scenarios.ts` names the starting states. The workbench ones share
fixed ids: session `ses-backoff` in workspace `ws-retries` of project
`proj-api`. The `agent-*` scenarios queue a scripted reply for the next
message: tool calls with streamed output, reasoning streamed before the
answer, a question with options, a provider failure, or a run that never
ends. Add a scenario when a screen
needs data no existing one has; build it with `WorldBuilder` in
`harness/world.ts`, and script replies as `ReplyStep` lists (`say` streams
prose, `think` streams reasoning, `tool` runs a call, `ask` waits for an
answer, `fail` ends the run, `hang` never ends it). A played reply also emits
`turn.progress`, so the status bar's context meter and decode rate are on
every screenshot of a session that has run.

## Regression baselines (`visual`)

`specs/*.spec.ts` compare screens with the PNGs in `__screenshots__/`:

```sh
npm run visual            # compare (make visual)
npm run visual:update     # accept the current rendering (make visual-update)
```

A failure writes `-expected`, `-actual`, and `-diff` PNGs under
`test-results/`; read the diff to see what moved. `npx playwright show-report
e2e/report` opens the HTML report. Specs use the same driver as `shot`, so a
flow tried from the command line becomes a spec by copying its steps:

```ts
test("settings", async ({ open, expectShot }) => {
  const eika = await open({ scenario: "workbench", theme: "dark" });
  await eika.click("Settings");
  await expectShot(eika, "settings-dark", "role=dialog");
});
```

`expectShot` also fails on console errors, page errors, and unmocked routes.
The threshold is 10 pixels, so a changed word fails.

Baselines are rendered by Chromium on Linux and have no platform suffix
(`snapshotPathTemplate` in `playwright.config.ts`), so one set serves every
Linux machine and CI. What keeps them stable across machines:

- Every font is bundled: IBM Plex Sans and JetBrains Mono come from
  `@fontsource-variable`, and each step waits for `document.fonts.ready`.
- The UI uses no character outside those fonts' bundled subsets, since a
  missing glyph is drawn by whatever system font has it. Draw a symbol such
  as ⌘ or ← with a lucide icon instead.
- The clock, the data, and the animations are fixed.

Font rendering on macOS or Windows differs, so regenerate baselines on Linux
rather than loosening the threshold. If CI's rendering drifts from a local
machine's, the failed job's `playwright-report` artifact holds the diffs.

## Checks and CI

`make visual` runs the suite: about four minutes on a laptop, one browser at
a time. `make check` runs it after the Go and web checks, so it takes about
five minutes in all. `.github/workflows/ci.yml` runs the same three parts as
separate jobs on `ubuntu-latest` (`go`, `web`, and `visual`). The visual job
installs Chromium with its system libraries (`PLAYWRIGHT_DEPS=--with-deps`),
and when it fails it uploads `e2e/report` and `e2e/test-results` as the
`playwright-report` artifact: download it and open the diffs, or run
`npx playwright show-report` on the report.

The specs run one browser at a time (`workers: 1`); pass `--workers=4` where
memory allows. They start their own Vite server on port 4319 and refuse to
reuse one already there; set `EIKA_VISUAL_PORT` to move it. The Account tab
shows that URL, so its baselines change with the port.
