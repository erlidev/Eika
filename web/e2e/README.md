# Visual testing

Drive the UI in a real browser against a **mock harness** and screenshot the
result. No Go server, Postgres, or Docker needed: `harness/mock.ts` answers
every `/api` route and the `/api/events` WebSocket from an in-memory world,
and plays scripted agent replies so a sent message streams like a real run.
Routes no UI calls are not served; `harness/mock.test.ts` lists each in
`unmocked`.

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
  "contractBreaks": [],
  "url": "..."
}
```

- Read the PNGs to see the page. `final.png` is taken after the last step.
- Read `final.aria.yml` to learn what a target can be called: it lists every
  button, tab, field, and heading by role and name.
- On a failed step `ok` is false, `failedStep` and `error` say why, and
  `failed.png` with `failed.aria.yml` show the page where it stopped.
- `unhandledApi` lists routes the UI called that the mock does not serve yet;
  add them to the resource's file in `harness/routes/`.
- `contractBreaks` lists what breaks the API contract (see below): a request
  the harness would refuse, or an answer or event it would never send.
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

A **target** is what the UI calls something: `Settings`, `New chat`,
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

`harness/scenarios.ts` names the starting states. Every world has the home
session `home-main`, as the harness makes it at start, and every scenario opens
at `/`, where Chat shows it. `home-empty` leaves it empty; `home-world` has it
with named subagents in different states, with stable IDs for session route
tests. `activity` extends it for the Activity page: two projects (Garden, Kitchen) and
subagents in every state, two waiting for different reasons, and a run going on
each running or waiting one so Cancel works (aborting it records the subagent
`cancelled`, as the harness does); it opens at `/activity`. The workbench ones share a story: computers in several states, session
`ses-backoff` in computer `cx-retries` (which the mock's own tests address), and
the same transcript on `home-main`, which is the one the UI can open.
`chat` queues a web search reply. `mcp` adds five MCP servers, one in each
state; the mock's authorization server approves at
once, so Sign in returns straight to `/mcp/callback`. `recorded-calls` adds
two recorded calls to `ses-backoff` for the context meter. The mock resolves
agent definitions and assembles requests in `harness/modelContext.ts`. Each run
it plays records its model call. The `agent-*`
scenarios queue a scripted reply for the next message: tool calls with
streamed output, reasoning streamed before the answer, a question with
options, an MCP server asking for input, a provider failure, or a run that
never ends. Add a scenario when a screen needs data no existing one has; build it with `WorldBuilder` in
`harness/world.ts`, and script replies as `ReplyStep` lists (`say` streams
prose, `think` streams reasoning, `tool` runs a call, `ask` waits for an
answer, `mcp` calls an MCP server's tool, which with `elicit` first asks the
user for input, `fail` ends the run, `cutOff` ends the turn on an incomplete stop
reason, `hang` never ends it). A played reply also emits `turn.progress`, so
the status bar's context meter is on every screenshot of a session that has
run.

## Specs (`visual`)

`specs/*.spec.ts` drive the UI through flows and assert what a person would
check: what is visible, enabled, focused, or described. Two kinds of
baseline back the assertions, each where it is the right tool:

- **Screenshots** (`expectShot`, PNGs in `__screenshots__/`) where pixels are
  the point: both themes of the session, phone layouts, the transcript's
  cards, the settings dialog. A few stray pixels fail it, so
  it catches a restyle nobody meant.
- **ARIA snapshots** (`expectAria`, YAML in `__aria__/`) where the point is
  what a screen says and offers: its headings, fields, buttons, which are
  disabled or invalid, and their text. A copy change shows as a readable
  text diff rather than a picture to squint at, and a restyle leaves it alone.

A baseline that only repeats a spec's assertions is churn, not coverage:
leave it out.

```sh
npm run visual            # compare (make visual)
npm run visual:update     # accept the current rendering (make visual-update)
npx playwright test --update-snapshots=missing   # write only new baselines
```

A failed screenshot writes `-expected`, `-actual`, and `-diff` PNGs under
`test-results/`; read the diff to see what moved. A failed ARIA snapshot
prints the text diff. `npx playwright show-report e2e/report` opens the HTML
report. Specs use the same driver as `shot`, so a flow tried from the command
line becomes a spec by copying its steps:

```ts
test("settings", async ({ open, expectShot, expectAria }) => {
  const eika = await open({ scenario: "workbench", theme: "dark" });
  await eika.click("Settings");
  await expect(eika.page.getByText("gpt-5-mini")).toBeVisible();
  await expectShot(eika, "settings-dark", "role=dialog");
  await eika.click("Account");
  await expectAria(eika, "account", "role=dialog");
});
```

Every test also fails, when it ends, on a console error, a page error, a
route the mock does not serve, or a break of the API contract.

### The API contract

`docs/api/contract.json` is the shape of every route's request and response
and every event's payload, written from the Go wire types by the server's
tests. `harness/contract.ts` reads it, and the mock holds itself to it on
every request: a body with a field the harness does not know, or no body
where it wants one, gets the 400 the harness would answer, and a reply or
event the harness could not send is reported. `harness/mock.test.ts` (run by
`npm test`) calls every route the mock serves, so a route no spec reaches is
held to the contract too. When the harness's types change, `make contract`
rewrites the file and these tests say what in the mock, and in
`src/api/types/`, has to follow.

### Stable baselines

Screenshots are rendered by Chromium on Linux and have no platform suffix
(`snapshotPathTemplate` in `playwright.config.ts`), so one set serves every
Linux machine. What keeps them stable across machines:

- Every font is bundled: IBM Plex Sans and JetBrains Mono come from
  `@fontsource-variable`, and each step waits for `document.fonts.ready`.
- The UI uses no character outside those fonts' bundled subsets, since a
  missing glyph is drawn by whatever system font has it. Draw a symbol such
  as ⌘ or ← with a lucide icon instead.
- The clock, the data, and the animations are fixed.

Font rendering on macOS or Windows differs, so regenerate baselines on Linux
rather than loosening the threshold. If a machine's rendering drifts from the
baselines', the run's report holds the diffs.

## Running the suite

`make visual` runs the suite in under a minute; `make check` runs it after
the other checks. The specs run a browser per two cores (`--workers=1` on a
machine short of memory), build the frontend into `e2e/dist`, and serve it
with `vite preview` on port 4319 (`EIKA_VISUAL_PORT` moves it; the Account
tab shows that URL, so its baselines change with the port).

## Compose smoke test (`smoke/`)

`make smoke` is the one test against the real stack. It starts compose as
project `eika-smoke` on port 18080 with `compose.smoke.yaml`, whose `model`
service runs `smoke/model.ts`, a scripted OpenAI-compatible endpoint. The
model lists `smoke`, answers a home message, and delegates the sandbox probe
to a worker. The worker calls `bash` to print `smoke-ok`, the hostname and
the working directory. `smoke/smoke.spec.ts` (config
`playwright.smoke.config.ts`) waits for the home computer, completes setup,
and sends the probe request in Chat. It reads the worker's session path and
checks the hostname against the home computer's container ID and the directory
against `/home/eika`. This proves that eikad ran the command in the container.
The stack builds its own sandbox image, `eika-sandbox:smoke`, and never reads
or moves
`eika-sandbox:latest`. `make smoke-down` removes the stack, its volumes, and
any sandbox left on its networks with the volume of each. A first run builds
the images and takes a few minutes; later runs take well under one.

## Measure transcript history

Run `EIKA_MEASURE_HISTORY=1 npm run visual -- e2e/specs/history-measurement.spec.ts --workers=1`
from `web/`. The test loads 5,000 entries in 50-entry pages, then scrolls for
three seconds. It prints each page's elapsed time, React render duration and rendered row
count, and total time in long tasks (50 ms or more). It also prints initial
load, frame count, frames above 20 ms, and the longest frame interval. The
measurement uses React's production profiling build. The mobile browser context emulates
screen size and touch; its timing is not a measurement on a physical phone.
