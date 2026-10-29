# Browser

The browser covers everything without a plugin. It is also where the most hostile
content enters: any page can carry instructions written for agents. So it is the
most isolated part of Eika. It runs in its own container, is controlled only
through Sentry, is driven by one agent with no memory, and shows the model page
text but never cookies, passwords or site code.

## 1. Container

`eika-browser` runs Chromium plus `browserd`.

- **`browserd`** is a small TypeScript service on Playwright. It lives in the repo
  at `browser/` with its own `Dockerfile`, tests and pinned dependencies.
  Playwright is already the frontend's test dependency, and it supplies the
  accessibility snapshot with element refs that agents navigate by.
- Alternatives that were considered:
  - Vercel's agent-browser has snapshots with refs, a vault, domain allowlists
    and a live viewport. Its vault and policy would duplicate Sentry's, and its
    token claims are unpublished.
  - Playwright MCP in the container behind Core's MCP client. Its tool surface
    (JavaScript evaluation among it) is wider than the one Eika wants to expose,
    and the custom parts below would still be needed.
  - Go with chromedp. That means writing the snapshot pruning that Playwright
    already has.
- **Networks.**
  - `eika_browser_ctl`: only Sentry reaches the control port.
  - `eika_browser_internal` (internal: true): all of Chromium's traffic goes
    through Sentry's proxy with the browser principal.
  - The viewer port (section 5) is on a network Core joins.
- **Hardening.**
  - Chromium runs as an unprivileged user with its own sandbox (`--no-sandbox` is
    not used: the container gets the seccomp profile Chromium needs).
  - DevTools and remote debugging are not reachable from outside the container.
  - Extensions are disabled.
  - Downloads land in `/downloads`, a quarantine directory (section 6).
- **Profile.** A persistent volume `eika-browser-profile` keeps cookies and site
  storage, so logins survive. The model never sees them. The user can wipe the
  profile, or a single site's data, from the Browser page.

## 2. Commands

`browserd` exposes a fixed command set over HTTP. Sentry relays each one as a
`browser` action, so Sentry decides every command before `browserd` receives it.
There is no JavaScript evaluation, no arbitrary CDP, and no file system access
beyond downloads.

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
| `tabs`, `tab {id}`, `close {id}` | read | tab list |
| `wait {for: text \| ref \| idle, timeout}` | read | delta |
| `downloads` | read | quarantined files with sizes and types |
| `upload {ref, path}` | write; the path must be a file on a computer Sentry allows | delta |

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
- **Gaps.** Iframes and open shadow roots are traversed. Closed shadow roots and
  canvas are listed as opaque regions with a suggestion to use `screenshot`.
  Accessibility trees, Markdown and raw HTML all miss those.

## 3. The browser agent

- The definition `browser` (`runtime.md` 6) is the only agent with browser tools.
  Others delegate a goal to it with `task_start {agent: browser, brief}`. One
  browser task runs at a time, holding the browser lock. Others queue. This
  matches "always controlled by a specific subagent".
- It has no memory, no plugins and no computer. Its brief carries what it needs,
  and it ends with `report {summary, outputs}`: extracted data, confirmation
  numbers, files downloaded.
- Its session holds A from the first page. Logged-in sites make it hold B too, so
  every state-changing action is reviewed or asked (section 4).
- Its results return to the delegator as untrusted content, wrapped. The main agent
  reads a scanned summary, never a page. The Dual LLM pattern calls this a
  quarantined model; injections can still shape the summary, which is why the
  label carries over.
- `screenshot` and vision are used only when the model is vision-capable and the
  snapshot names an opaque region or keeps failing. Browser Use advises disabling
  vision around secrets, and so does `browserd`: screenshots are refused while a
  masked field on the page has focus.

## 4. What Sentry checks

| Situation | Verdict |
|---|---|
| Navigate, read, find, snapshot, scroll, tab | allow, subject to the domain policy below |
| `click` on a link | allow |
| `click` on a submit button, or `type ... submit`: Sentry asks `browserd` for the form's method, action URL and filled fields, and decides on that | `review`. `ask` from A+B, or when the reviewer flags it. Sending a message or posting is `send external` |
| A form detected as account creation (new-password fields, a second password confirm, sign-up wording, `autocomplete=new-password`), password change, 2FA setup, payment fields, accepting terms | `handoff`. The approval card offers takeover |
| `type` with a surrogate | Resolved only when the page's origin is in the secret's bound domains, and only into a field of a matching type |
| A download | allow into quarantine. Moving it to a computer is a `write` action on `upload` or on a separate `browser_download_to {file, computer path}` |
| Domains | A per-site list in the Browser page: `allow`, `ask` or `block`. Default allow for navigation, with known-bad lists in Guard later. The proxy's taint rule applies to POSTs to hosts not seen before |

`browserd` also intercepts requests (Playwright routing) and reports every
non-GET to Sentry for an `egress` decision before it leaves. This catches a form
that submits through script, and the `sendBeacon` and `fetch` exfiltration paths
that a host-level proxy cannot see.

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
- **Hand-offs.** Account creation, logins with 2FA, CAPTCHAs and payments arrive
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
and the file keeps its untrusted label: a task that reads it gains A.

## 7. Testing

- `browser/` unit tests: snapshot pruning, masking, budget cutting and deltas, run
  against fixture pages.
- A Docker test with Sentry and a fixture site container:
  - a login through a surrogate, checking that the password never appears in any
    response to Core
  - a sign-up form handed off
  - a submit asked
  - an exfiltration attempt by `fetch` blocked
- `evals/browser` (opt-in): a small WebArena-style task set on local fixture
  sites. Each task has injected variants: hidden text, an alt attribute,
  instructions in a review. Results record success, tokens per step and attack
  success.
