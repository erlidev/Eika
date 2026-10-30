# Browser

The browser covers everything without a plugin. It is also where the most hostile
content enters: any page can carry instructions written for agents. So it is the
most isolated part of Eika. It runs in its own container, is controlled only
through Sentry, is driven by one agent with no memory, and shows the model page
text but never passwords or stored session cookies.

## 1. Container

`eika-browser` runs real Google Chrome plus `browserd`, on a virtual display.

- **Engine.** Chrome stable, installed from Google's package and pinned in the
  image, runs **headed** on Xvfb. It is not headless and not Playwright's bundled
  Chromium. Sites that gate on captchas and bot scores flag headless mode,
  bundled builds and the usual automation tells most often, and benchmarks
  repeatedly show that a real, headed Chrome passes far more of them
  (see the table below).
- **`browserd`** is a small TypeScript service on **Patchright**, a Playwright
  fork (Apache-2.0) that removes the automation tells at the protocol level:
  the `Runtime.enable` leak, the console and `navigator.webdriver` signs, and
  closed shadow roots, which it can pierce. It keeps Playwright's API, so the
  accessibility snapshots with element refs that agents navigate by carry over.
  It lives in the repo at `browser/` with its own `Dockerfile`, tests and pinned
  dependencies. Chrome runs with a persistent context and `channel: chrome`.
- **Networks.**
  - `eika_browser_ctl`: only Sentry reaches the control port.
  - `eika_browser_internal` (internal: true): all of Chrome's traffic goes
    through Sentry's proxy with the browser principal. The proxy tunnels TLS
    without intercepting it, so sites see Chrome's own TLS fingerprint and the
    user's own IP.
  - The viewer port (section 5) is on a network Core joins.
- **Hardening.**
  - Chrome runs as an unprivileged user with its own sandbox (`--no-sandbox` is
    not used: the container gets the seccomp profile Chrome needs).
  - DevTools and remote debugging are not reachable from outside the container.
  - WebRTC is limited to the proxy path, so the real address does not leak.
  - Extensions are disabled.
  - Downloads land in `/downloads`, a quarantine directory (section 6).
- **Profile.** A persistent volume `eika-browser-profile` keeps cookies and site
  storage, so logins survive and the browser builds up the history of a normal
  one. The model never sees stored cookies. The user can wipe the profile, or a
  single site's data, from the Browser page.
- **Platforms.** Chrome only. Google ships Chrome stable for x86-64 Linux, so
  that is the supported target for the browser container. There is no
  Chromium or second-engine fallback.

### Choosing the browser

Considered, with the evidence (all of it 2026 third-party benchmarks run from one
or a few IPs, so directional and not guarantees; the detection arms race
continues and every option needs updating):

| Option | Verdict |
|---|---|
| **Patchright on real headed Chrome** | **Chosen.** One benchmark had it pass every protection system tested when headed (and 40% headless); another had 25 of 31 protected sites open, with the move to system Chrome responsible for much of the gain over vanilla Playwright. Playwright API, Apache-2.0, the most actively maintained of the patched forks, TypeScript |
| nodriver (raw CDP, system Chrome) | Best single result in one benchmark (28 of 31, the only tool through a Cloudflare Turnstile site). Python, AGPL-3.0, no Playwright API, so snapshots, refs and `browserd` would be rewritten. Kept as the fallback if Patchright degrades |
| Camoufox (patched Firefox) | Strong against fingerprinting, the best headless result in one benchmark, but heavy on CPU, a Firefox engine (sites tuned for Chrome behave differently) and a slower release cadence. Not used: Chrome only |
| CloakBrowser (patched Chromium build) | Comparable results; a small project, and its builds lag on some platforms |
| Playwright/Puppeteer plus stealth plugins | Largely broken by current detection. rebrowser-playwright was last updated in 2024 |
| Vanilla headless Chromium | The previous design. Flagged by many sites |
| Hosted browsers (Browserbase and others) | Better evasion, but the profile, logins and every page leave the machine. Contradicts local first and the vault model. Not used |
| Vercel's agent-browser (Rust CLI and daemon, raw CDP, Apache-2.0) | A good agent-facing layer (ref snapshots, `--delta`, `eval`, annotated screenshots, a JPEG stream), and it can drive system Chrome headed on Xvfb. It has no stealth work of its own, so it adds nothing for captchas. Its vault, domain allowlist and cloud-provider support duplicate Sentry or break local first, and the per-command CLI gives Sentry no hook for form inspection, origin-bound typing or the 6,000-token snapshot budget. Ideas borrowed, not adopted |
| Playwright MCP | Wider tool surface than wanted, and policy that would duplicate Sentry's |

Captchas are not special-cased. The browser agent attempts one like any other
page element, with the tools it has (clicking, typing, `screenshot` on a vision
model). Takeover is there when it gets stuck, as for any page. A paid solver
service is not planned, since it would send page content to a third party.

## 2. Commands

`browserd` exposes a fixed command set over HTTP. Sentry relays each one as a
`browser` action, so Sentry decides every command before `browserd` receives it.
There is no arbitrary CDP and no file system access beyond downloads.
JavaScript runs through `eval` (below).

| Command | Effect | Returns |
|---|---|---|
| `open {url}`, `back`, `forward`, `reload` | read | snapshot |
| `snapshot {mode: interactive \| full, root?: ref, delta?}` | read | snapshot text |
| `find {text \| role, name?}` | read | matching refs with context lines |
| `read {ref? \| main}` | read | the region as Markdown (Readability plus Turndown in `browserd`) |
| `click {ref}` | see section 4 | snapshot delta |
| `type {ref, text \| secret: surrogate, submit?}` | write (a secret is `credential`, resolved by Sentry) | delta |
| `select {ref, option}`, `check {ref}`, `scroll {ref? \| page}`, `hover {ref}` | write or read | delta |
| `screenshot {ref? \| viewport}` | read | image (vision models only) |
| `eval {script, ref?}` | write, always allowed (section 4) | the JSON-serialisable result, scanned and wrapped like page text |
| `tabs`, `tab {id}`, `close {id}` | read | tab list |
| `wait {for: text \| ref \| idle, timeout}` | read | delta |
| `downloads` | read | quarantined files with sizes and types |
| `upload {ref, path}` | write; the path must be a file on a computer Sentry allows | delta |

### JavaScript

`eval {script, ref?}` runs a script in the page, as an async function with the
element for `ref` as its argument, and returns its result. It is what makes the
browser powerful: extracting data from a table in one call, reading page state
the accessibility tree hides, and driving widgets that refuse ordinary clicks.

- Scripts run in an isolated world by default, so page scripts cannot see or
  interfere with them and they leave no trace for detection to find. A flag runs
  one in the page's main world when it needs the page's own variables.
- Results are capped (default 50k characters), scanned and wrapped like page text.
- A script can do anything page script can, including reading non-HttpOnly
  cookies and sending requests. Section 4 covers how it is decided.

### Snapshots

- The default is an **interactive snapshot**: a pruned accessibility tree of
  controls and headings, with refs.

  ```
  [page] Checkout - shop.example  https://shop.example/checkout
  - heading "Shipping address"
  - textbox "Full name" [e3] value="Sam Lee"
  - textbox "Card number" [e7] (masked)
  - button "Place order" [e12]
  - link "Back to cart" [e14]
  (+38 links; find or snapshot full to see them)
  ```

- **Budget.** Snapshots are capped at 6,000 tokens. Over the cap, the snapshot
  keeps the top of the page and the controls nearest the last action, and reports
  what it left out. This addresses the cost the Stagehand benchmark measured: a
  500-row table runs to about 80k tokens as a full accessibility tree. `find` and
  `read` reach the rest.
- **Deltas** after an action list only what changed, unless the page navigated.
- Password fields, and fields whose `autocomplete` is a credential or card type,
  always show as `(masked)`, whatever their value.
- **Gaps.** Iframes and shadow roots, closed ones included (Patchright pierces
  them), are traversed. Canvas is listed as an opaque region with a suggestion to
  use `screenshot`. Accessibility trees, Markdown and raw HTML all miss it.

## 3. The browser agent

- The definition `browser` (`runtime.md` 6) is the only agent with browser tools.
  Eika delegates a goal to it with `subagent_start {agent: browser, name, brief}`.
  One browser subagent runs at a time, holding the browser lock for the length of
  its run. Others queue. This matches "always controlled by a specific subagent".
- It has no memory, no plugins and no computer. Its brief carries what it needs,
  and it ends with `report {summary, outputs}`: extracted data, confirmation
  numbers, files downloaded.
- Every page it reads is scanned and wrapped. Logged-in sites make its session
  `sensitive`, so every submit and every `eval` there goes to the reviewer with
  stricter thresholds (section 4), and to the user when the reviewer is unsure.
- Its results return to the delegator as untrusted content, wrapped. The main agent
  reads a scanned summary, never a page. The Dual LLM pattern calls this a
  quarantined model; injections can still shape the summary, which is why the
  wrapper and any `sensitive` label carry over.
- `screenshot` and vision are used only when the model is vision-capable and the
  snapshot names an opaque region or keeps failing. Browser Use advises disabling
  vision around secrets, and so does `browserd`: screenshots are refused while a
  masked field on the page has focus.

## 4. What Sentry checks

| Situation | Verdict |
|---|---|
| Read, find, snapshot, scroll, tab | allow |
| Navigate (`open`, `back`, `forward`, `reload`) | `allow` in a clean session, subject to the blocklist. `review` in a labelled one, since a URL can carry data out |
| `click` on a link | allow |
| `click` on a submit button, or `type ... submit`: Sentry asks `browserd` for the form's method, action URL and filled fields, and decides on that | `allow` in a clean session. `review` in a `sensitive` or `suspicious` session, with stricter thresholds, and `ask` when the reviewer is unsure. Sending a message or posting is `send external` |
| A form detected as account creation (new-password fields, a second password confirm, sign-up wording, `autocomplete=new-password`), password change, 2FA setup, payment fields, accepting terms | `handoff`. The approval card offers takeover |
| `type` with a surrogate | Resolved only when the page's origin is in the secret's bound domains, and only into a field of a matching type |
| A download | allow into quarantine. Moving it to a computer is a `write` action on `upload` or on a separate `browser_download_to {file, computer path}` |
| `eval` | `allow` in a clean session. `review` in a labelled one: the reviewer reads the agent-written script (capped at 16k tokens). Scripts on pages are never reviewed |
| Sites | The blocklist (`sentry.md` 5), edited from the Browser page, with known-bad lists in Guard later. Everything else is allowed |

`browserd` does not report individual requests, and the egress proxy does not judge
connections (`sentry.md` 10): a page load opens dozens, and judging each would
cost more than the session. In a labelled session the decisions sit on the actions
that can move data out: navigating, submitting and `eval`. What that leaves open
is a page script that sends data to a host on its own, with no action of the
agent's behind it.

**Identifying the agent.** By default Eika browses as the user, from the user's IP,
with a normal user agent. That has advantages, and it carries an obligation. The
setting `browser.identify` (off by default) adds `Eika-Agent` to the user agent.
The design notes Amazon's blocking of Muse in September 2026, and Duet's choice
to publish its user agent and give sites an opt-out, so the user can decide.

## 5. Live view and takeover

- **Viewer.** `browserd` streams a CDP screencast (`Page.startScreencast`, JPEG
  frames) over a WebSocket on its viewer port. Core relays it to the UI after
  checking a one-time ticket from Sentry, using the pattern of today's preview
  tickets. No X server or VNC is involved, and coordinates map exactly.
- **Watching** is read-only and needs no lock. It works on a phone: the frame
  scales to the screen, with pinch-zoom.
- **Takeover.** The user presses Take over. Sentry gives the lock to the user, and
  the browser agent's commands return "the user has control; wait". The agent's
  tool parks. Input events (pointer, keys, text, scroll) go from the UI through the
  viewer socket to CDP `Input.dispatch*`. Return control gives the lock back. The
  agent gets a notice and a fresh snapshot, with no claim about what the user did.
- **Hand-offs.** Account creation, logins with 2FA and payments arrive
  as an Inbox card. The card holds the live view with takeover already offered.
  Sign-in through the vault (`type` with a surrogate) means the user types a
  password once, in the secure form, and Eika logs in after that without seeing it.
- **Gaps.** Native file pickers, permission prompts and print dialogs do not appear
  in a screencast. `browserd` disables them: uploads go through `upload`, and
  permissions are denied unless the user allows a site on the Browser page.

## 6. Downloads

Files land in `/downloads` with a size cap (default 100 MiB per file). Sentry
records type, size and SHA-256. Text-like files can be read through ingest as
untrusted content. Moving a file to a computer requires the action noted above,
and the file stays untrusted: reading it goes through ingest like any web content.

## 7. Testing

- `browser/` unit tests: snapshot pruning, masking, budget cutting and deltas, run
  against fixture pages.
- A Docker test with Sentry and a fixture site container:
  - a login through a surrogate, checking that the password never appears in any
    response to Core
  - a sign-up form handed off
  - a submit asked
  - an `eval` from a `sensitive` session that posts private data to a new host,
    and a navigation to a URL carrying it, each going to the reviewer and denied
    by a scripted decision model
- `evals/browser` (opt-in): a small WebArena-style task set on local fixture
  sites. Each task has injected variants: hidden text, an alt attribute,
  instructions in a review. Results record success, tokens per step and attack
  success.
