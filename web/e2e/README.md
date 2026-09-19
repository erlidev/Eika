# Visual testing

Drive the UI in a real browser against a **mock harness** and screenshot the
result. No Go server, Postgres, or Docker needed: `harness/mock.ts` answers
every `/api` route and the `/api/events` WebSocket from an in-memory world,
and plays scripted agent replies so a sent message streams like a real run.

Chromium is needed once: `npx playwright install chromium`.

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

### Steps

One action per `--step`, or one per line in a `--steps <file>`:

| Step                                           | Does                                                             |
| ---------------------------------------------- | ---------------------------------------------------------------- |
| `click <target>` / `hover <target>`            | Click or hover it.                                               |
| `fill <target> = <value>`                      | Replace a field's text.                                          |
| `select <target> = <option>`                   | Pick from a native or Radix select.                              |
| `press <key>` / `type <text>`                  | Keyboard: `Enter`, `Escape`, `Control+K`; text into the focus.   |
| `send <message>`                               | Type into the session composer and send. The mock agent replies. |
| `wait <ms or target>`                          | Pause, or wait until something is visible.                       |
| `goto <path>` / `reload`                       | Navigate.                                                        |
| `theme light\|dark` / `viewport 390x844`       | Change how the page renders.                                     |
| `emit <event json>`                            | Push an event from docs/api/events.md onto the stream.           |
| `shot <name> [= <target>]` / `fullshot <name>` | Screenshot the viewport, one element, or the full page.          |
| `aria`                                         | Save the accessibility tree as `page.aria.yml`.                  |

A **target** is what the UI calls something: `Settings`, `New project`,
`Password`, `30s`. It is tried as a button, link, tab, menu item, option,
tree item, checkbox, switch, radio, and combobox name, then a field label, a
placeholder, and exact text. Use a Playwright selector when a name is
ambiguous: `role=dialog`, `role=button[name="Delete gpt-5"]`, `text=/rate limit/`,
`label=Password`, `css=.foo`.

Every step waits for the page to settle (the mock idle, a scripted reply
finished, fonts loaded, animations done), so no `wait` is needed after one.
The clock is fixed at `2026-03-14T15:00Z` so relative times never change.

## Scenarios

`harness/scenarios.ts` names the starting states. The workbench ones share
fixed ids: session `ses-backoff` in workspace `ws-retries` of project
`proj-api`. The `agent-*` scenarios queue a scripted reply for the next
message: tool calls with streamed output, a question with options, a
provider failure, or a run that never ends. Add a scenario when a screen
needs data no existing one has; build it with `WorldBuilder` in
`harness/world.ts`, and script replies as `ReplyStep` lists.

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
Baselines are rendered by Chromium on Linux; font rendering on another OS
differs, so regenerate them there rather than loosening the threshold.
