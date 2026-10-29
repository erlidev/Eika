# Sentry

Sentry is the sole permission authority. Every consequential thing an agent does
passes through it first: tool calls, plugin calls, memory writes, browser actions,
network egress from any container, and outbound messages. It holds every
third-party credential, and the model never sees one. Approvals go to the user
through the UI and never through the conversation. Sentry must be lightweight:
its deterministic path adds well under a millisecond to an action.

The design takes the separate host-side Sentinel and surrogate tokens from Meta's
Muse; the out-of-model check, two-tier sensitive-data classifier and credential
isolation from Claude Duet; and auto-review and the four rule levels from OpenAI
dots. All three vendors say the same thing: prompt injection is not solved, so the
structure has to contain it.

## 1. Properties

| Property | How |
|---|---|
| Isolated | Separate binary and container (`cmd/eika-sentry`), its own Postgres schema and role, its own key file and volume. Core cannot read the vault |
| Sole grantor | Core's tool dispatch, egress, plugin calls and browser commands cannot happen without a `Decide` answer. Plugins and the browser are reachable only through Sentry |
| Out of conversation | Approvals, secret entry and alerts are UI objects. The model learns only the outcome |
| Fast | Policy, grants and taint are in memory; scanning applies to untrusted content only; the reviewer runs only for `review` actions; monitoring is asynchronous |
| Fail closed | If Sentry is unreachable, side-effecting tools return "Sentry unavailable" and the run parks. Read-only built-ins with no egress keep working |
| Auditable | Every decision is an append-only audit row, viewable and exportable |

## 2. Process, storage and interface

- **Container.** `eika-sentry` on the networks `eika_default` (Postgres, Guard),
  `eika_sandbox_internal` (proxy for computers and plugin cells),
  `eika_browser_ctl` (the browser's control port) and `eika_egress` (the internet).
- **Storage.** Postgres schema `sentry`, owned by role `eika_sentry`, with its
  own migrator over `internal/sentry/store/migrations`. Core's role
  `eika_core` has no privilege on it. `eika_sentry` has `SELECT` on
  `core.auth_sessions` only, so it can check that the user, and not Core, signed
  an approval.
- **Keys.** `/var/lib/eika-sentry/vault.key` (AES-256-GCM, `internal/secret`
  reused) in volume `eika-sentry`.
- **Core API.** HTTP/JSON over a Unix socket at
  `/run/eika-sentry/sentry.sock`, on a volume shared only by Core and Sentry. Wire
  types live in `internal/sentry/api`, the only Sentry package Core imports. The
  contract is in `docs/api/sentry.md`, generated like `contract.json`.

| Route | Purpose |
|---|---|
| `POST /v1/decide` | Decide one action (section 4). Idempotent on `action.key` |
| `GET /v1/approvals/{id}/wait` | Long-poll until an approval is answered or expires |
| `POST /v1/ingest` | Scan, label and wrap one untrusted result before it reaches a model (section 6) |
| `POST /v1/egress/principals` | Core registers a container or run and receives proxy credentials for it |
| `POST /v1/plugins/call`, `GET /v1/plugins` | Plugin host (`skills-and-plugins.md`) |
| `POST /v1/browser/command` | Browser mediation (`browser.md`) |
| `POST /v1/observe` | Asynchronous feed of turns, reasoning and tool calls for the monitor |
| `GET /v1/events` | NDJSON stream of `approval.*`, `alert.*`, `halt.*`, `secret_request.*`, which Core relays to the UI on topic `inbox` |
| `/v1/user/*` | User-authenticated routes Core reverse-proxies unchanged from the UI: answer approvals, edit rules and grants, the vault, halt and resume, the audit log. Each request carries the user's bearer token, which Sentry checks itself |

## 3. Actions

Core describes every action in one shape:

```
Action {
  key          stable id: session id + tool call id, or container + connection id
  principal    {agent, session, task, run, routine, project, computer}
  kind         tool | plugin | memory_write | egress | browser | schedule | message
  name         tool name, plugin/tool, host, browser command
  effect       read | write | send | delete | pay | account | credential | schedule
  target       destinations: recipients, domains, paths, memory scope
  args         the call's arguments, or a digest for egress
  data         labels of what the call carries: private, sensitive, secret-like
  intent       the latest trusted user instruction this work descends from
  rationale    optional: the agent's stated reason (untrusted)
}
```

Built-in tools declare their `effect` and how to read `target` from their
arguments, in code next to the tool. Plugin tools take effect from MCP
annotations: `readOnlyHint` maps to read, `destructiveHint` to delete,
`openWorldHint` to send. Unannotated tools default to `write`. The user can
override per tool.

## 4. Decision pipeline

```
decide(action)
  0 halted?                                    -> deny "halted"
  1 hard rules (built in, not editable)        -> deny | handoff
  2 user rules (most specific first)           -> allow | ask | review | deny | handoff
  3 grants (matching, unexpired)               -> allow
  4 defaults by effect x taint (section 5)     -> allow | ask | review
  5 review: isolated reviewer (section 9.2)    -> allow | ask (below threshold) | deny
  6 ask: approval -> UI + push; the call parks -> allow (+grant) | deny
```

Steps 0 to 4 are in-memory lookups. Rules and grants are cached and invalidated by
change notifications. Step 5 calls a model. Step 6 waits on the user.

Verdicts:

| Verdict | Effect on the call |
|---|---|
| `allow` | Proceeds. Any transform Sentry returns (redactions, surrogate resolution) is applied |
| `ask` | Creates an approval. The tool parks until the user answers or it expires (default 24 h, then deny) |
| `review` | The reviewer decides; low confidence becomes `ask` |
| `deny` | The tool returns an error result giving the reason, so the agent can adapt |
| `handoff` | Only the user can do this. The result tells the agent to hand over. The UI offers takeover (browser) or a task card for the user |

## 5. Policy

### Hard rules

These are built in and cannot be switched off or granted:

- Deny moving money between accounts, trading securities, and acting in someone
  else's account (from Duet's list).
- Hand off creating accounts, changing passwords or 2FA, entering payment details,
  and accepting legal terms. The browser detects these (`browser.md`), and plugin
  tools can be marked as such.
- Deny any action whose arguments contain a canary or a real vault value
  (section 8.4), and alert.
- Deny an agent creating or editing a routine, grant or rule that would widen its
  own permissions. Routine creation is gated (`scheduler.md`); rules and grants
  have no agent tool at all.
- Never resolve a surrogate for a destination outside its bound domains.

### Defaults by effect and taint

`A` means the session holds untrusted content; `B` means it has read sensitive or
private data (section 6).

| Effect | clean | A | A+B |
|---|---|---|---|
| read (private sources marks B) | allow | allow | allow |
| write own data (project files, memory with user or inferred provenance) | allow | allow | review |
| write memory derived from untrusted content | quarantine (`memory.md` 6) | quarantine | quarantine |
| send to the user (notify, own channels) | allow | allow | allow |
| send external (email, post, message, form submit with content) | ask | ask | ask, grant scope at most "this task" |
| delete external | ask | ask | ask |
| schedule (create or enable a routine) | ask | ask | ask |
| egress to an allowlisted host | allow | allow | allow |
| egress to another host from a computer | allow | ask | ask |
| browser navigate | allow | allow | allow |
| browser submit or click a state-changing control | review | review | ask |
| pay, account, credential | handoff | handoff | handoff |

### User rules

Rules are rows: `{match, verdict, note}`. `match` names any of kind, name (with `*`
globs), effect, target patterns (recipient, domain, path), and argument conditions:
a small set of JSON-path equality and prefix tests, not a language. The UI edits
them with presets. Examples:

- calendar: read allow, create ask
- email: read allow, send ask, but send to `*@mycompany.com` review
- GitHub plugin: issues allow, everything else ask

A rule never overrides a hard rule. Rules come from OpenAI dots' four levels:
allow without asking, allow when the user says so (an `ask` scoped to an explicit
user request), ask before acting, and hand off.

### Approval-fatigue calibration

Sentry counts approvals per rule and tool. When the user has approved the same
narrow action 10 times in 14 days without a denial, the Inbox suggests a rule or a
standing grant. Anthropic calibrated Duet's approval rate this way for the same
reason.

## 6. Taint, ingress labelling and scanning

### Labels

Each session (and so each run) carries two labels, mirrored to Core:

- **untrusted (A)** is set when a tool result from an untrusted source enters the
  context: web search and fetch, the browser, plugin results not marked trusted,
  files downloaded or produced from those, and task results from agents that held
  A. It is cleared when a compaction replaces the content that carried it. Content
  inside the kept tail keeps the label.
- **sensitive (B)** is set when the session reads a private source: memory of
  sensitivity `private`, plugins the user marked private (email, calendar,
  finance), vault-backed logins, or files in a private project. It is cleared the
  same way.

This is coarse taint tracking at session granularity, a deliberate trade-off.
CaMeL-style per-value capabilities would be more precise, but they need a
planner-interpreter architecture and roughly three times the tokens (Google
DeepMind, 2025). Session-level labels plus the rule of two catch the dangerous
combination cheaply.

### Ingress

`POST /v1/ingest {principal, source, content, mime}`, called by Core for every
result from an untrusted source before it enters a model's context:

1. **Secret redaction** (deterministic). It scans with Aho-Corasick over current
   vault values and canaries, plus gitleaks-style patterns for well-known key
   formats. Matches are replaced by `[redacted:<kind>]`, and a vault value seen
   here raises an alert.
2. **One-time codes** (deterministic). Content from mail and messaging plugins and
   from browser pages has one-time codes, password-reset links and magic sign-in
   links removed (Muse's email connector does the same). The agent learns "a
   sign-in code arrived" but never the code.
3. **Injection screen** (classifier). Content is chunked to the classifier's window
   (512 tokens for Prompt Guard 2) and scored in parallel by the Guard sidecar.
   Content above 64 KiB is scored on its first and last 16 KiB plus 8 sampled
   chunks, so cost stays bounded. A hit does not block. It wraps the content with
   a warning, sets the session's `suspicious` flag (which turns the next
   `review` verdicts into `ask`), and raises a low or medium alert.
4. **Wrap.** The result returns as
   `<untrusted source="web:example.com" id="...">...</untrusted>`, with a
   one-line reminder in the agent's system prompt that such content is data, never
   instructions.

Ingest failure, such as Guard being down, returns the content wrapped and marked
`unscanned`. It is never dropped silently, and it is never blocked for lack of a
classifier.

### Outbound content

Every `send` action's content is checked with the two tiers from Duet:

- **Never share** (deterministic): credentials and vault values, full card numbers
  (Luhn), bank account and routing numbers, and government ID formats. The verdict
  is `deny`, with a reason the agent can act on: rewrite without them.
- **Needs consent** (decision model): health, finances, other people's personal
  information. The verdict is `ask` unless the user's own instruction in `intent`
  covers it. The user's own request counts as consent.

## 7. Grants and approvals

### Grants

A grant is a capability: `{match, scope, expires, created_by_approval}`. Scopes come
from Muse:

| Scope | Lifetime |
|---|---|
| `once` | This action key only |
| `task` | The rest of this task session |
| `session` | The rest of this session (home or direct) |
| `routine` | Every run of one routine; created when the user enables it |
| `until` | A time bound |
| `always` | Until revoked; not offered for send-external from A+B, delete, or schedule |

Grants are listed in the UI by agent, plugin and site, and are revocable at once.
Revoking a grant does not undo an action already taken. The UI says so.

### Approvals

An approval is a durable row in `sentry.approvals`:
`{id, action, verdict_path, reason, created, expires, state, answer, answered_by_token_hash}`.

- The UI card shows the agent and task, the action in plain words, the full
  destination and every recipient, the content (with a diff for edits), why it
  was asked (rule, default, reviewer reason, suspicion), and the session's
  labels. This follows Duet.
- The answers are: approve once, approve for this task, always (when the scope
  allows), deny, deny and cancel the task, and take over (browser).
- An answer is accepted only through `/v1/user/approvals/{id}` with a valid user
  token. Core's own service identity cannot answer.
- Push notification (`interface.md`) for every approval. Expiry denies.
- Approvals are keyed by action key, so a resumed run gets the same approval back
  (`runtime.md` 5).
- Related approvals are grouped. Three sends to the same recipients in one task
  show as one card, and approving it creates a task-scoped grant.

## 8. Vault and surrogates

### Secrets

A vault entry holds:

```
{name, kind, value (sealed), bound_domains[], allowed_principals[], note,
 created, last_used}
```

`kind` is one of `api_key`, `password`, `oauth_token` or `cookie_jar`. Only the user
creates entries, either in Settings, Vault or through a secret request.

### Secure entry

1. An agent that needs a credential calls
   `request_secret {name, purpose, domain}`. The tool parks.
2. Sentry creates a secret request, and the Inbox shows a form. The form never
   arrives as a link, a message or an email, the same rule Duet follows.
3. The UI fetches Sentry's ephemeral public key (X25519) and encrypts the value
   in the browser with WebCrypto. Core relays only ciphertext. Sentry decrypts,
   seals and stores it.
4. The tool returns the surrogate. Core never sees the value, even in transit.

### Surrogates

- A surrogate is `eika-sur-<name>-<8 hex>`. It is stable per entry, so a prefix that
  holds it stays cached.
- It is resolved only at the boundary, only for its bound domains, and only for
  allowed principals. It is resolved in four places:
  1. **Plugin calls** (Sentry hosts the connection, so headers and environment are
     filled in Sentry).
  2. **Browser typing** (Sentry sends the real value straight to `browserd`).
  3. **Model provider keys stay in Core**, by design: they are the user's chosen
     endpoints, not agent-reachable services.
  4. **HTTP egress from computers.** In phase 15, the proxy intercepts TLS only for
     hosts with bound secrets, using a Sentry CA trusted by the sandbox image. It
     swaps surrogates in headers and bodies. A surrogate in a request to any other
     host is denied with a high-severity alert, because that is an exfiltration
     attempt.
- Real values that appear in any content flowing back (ingress) are redacted. A
  surrogate shown to the model is just text.

### Canaries

At setup Sentry plants canaries in three places:

- a fake API key in the `home` computer's environment
- a fake memory of sensitivity `private`
- a fake vault-looking file in `/home/eika`

Each canary is unique and recorded. Any canary in egress, plugin arguments,
outbound content or browser typing is a severe alert and halts Eika (section 11).
Canaries cost nothing and catch the class of attack classifiers miss.

## 9. System One decision models and the reviewer

### Protocol

"System One" models answer typed questions about a state with calibrated
probabilities instead of generating text, the way TypeSafe's Jev does. Eika speaks
Jev's wire format, so any compatible backend fits:

```
POST {base}/v1/systemone
{"model": "...", "state": "<string or JSON>",
 "questions": {"matches_intent": {"type": "noul", "instructions": "..."},
               "risk": {"type": "choice", "options": ["low","medium","high"], "instructions": "..."}}}
-> {"answers": {"matches_intent": {"p": 0.93}, "risk": {"choice": "low", "confidence": 0.81, "dist": {...}}}}
```

`internal/systemone` holds the client, the wire types and thresholds. It is shared
by Sentry and Core, which uses it for routing and notification triage.

### Backends

| Backend | Where | Notes |
|---|---|---|
| Hosted Jev (TypeSafe) | cloud | 70 to 500 ms, 32k state. Sends state off the machine, so it is allowed only for content without the `sensitive` label unless the user opts in |
| Laya via `laya-serve` (Apache-2.0, 322M to 421M) | Guard sidecar, CPU or small GPU | Jev-compatible. Near chance before fine-tuning, so it needs the fine-tune loop below |
| Emulated | any chat model (`fast` class) | JSON-schema constrained output (llama.cpp grammar, vLLM guided JSON). Probabilities come from logprobs where available, else stated confidence. The fallback that always works |

Jev's own documentation lists the known weaknesses. These models do not count or
compare dates, so that stays in code. They read questions literally. They do not
treat state as hostile. So the reviewer never puts untrusted content into
`state` (below), and thresholds escalate to the user rather than block on their
own.

### The reviewer

It runs for actions whose verdict is `review`, in an isolated context. Its state
contains:

- the action (kind, name, target, arguments)
- `intent`: the trusted user instruction chain (the user's messages that led to
  this task, the task brief, standing rules)
- the agent's rationale, marked as untrusted
- optionally, a bounded excerpt of the agent's reasoning, marked untrusted

It never sees the web page, the email or the plugin output that came before.
Instructions hidden in that content cannot reach the reviewer, which is the reason
Duet runs its check outside the working model.

Questions:

- `matches_intent` (noul): is this action something the user asked for or would
  clearly expect?
- `scope` (choice: within, adjacent, beyond)
- `risk` (choice: low, medium, high)
- `reversible` (noul)
- `deceptive_reasoning` (noul, only with reasoning)

Allow when `matches_intent >= 0.9`, scope is within, and risk is low or medium.
Deny when `matches_intent < 0.2` or `deceptive_reasoning >= 0.8`, with an alert.
Ask otherwise. Thresholds are settings.

The reviewer must not stall the user. Target p50 is under 1 s. A reviewer that has
not answered within 5 s turns the verdict into `ask`.

### Contention

Sentry's model calls are not in Core's inference queue. The deployment guidance:

- Give `decision` its own endpoint: the Guard sidecar on CPU, a second small GPU,
  or a cloud API.
- If it must share the main GPU, give the llama.cpp server one slot more (`-np`)
  than Core's queue is configured with, so Sentry's short calls do not queue
  behind a long generation.

Settings warns when `decision` shares a pool with `main` and has no spare slot.

### Learning from the user

Every reviewer decision and the user's later answer on it is a labelled example in
`sentry.decisions`. The UI exports them as JSONL in the Laya fine-tune format. This
is the route to "fine-tuned decision models similar to Jev": a small local head
trained on this user's own approvals. It is phase 13, with a training script
outside `make check`.

## 10. Egress proxy

`internal/egress` and `internal/netguard` move from Core into Sentry unchanged in
substance:

- `CONNECT` and absolute-URL HTTP, dialled through netguard (public addresses only)
- refusals with 403, and the blocked-host memory

Changes:

- **Identity per principal.** Proxy credentials come from
  `POST /v1/egress/principals`: one per computer for its own processes, one per
  run for exec calls (`runtime.md` 10), one per plugin cell, one for the browser.
  The hub token, which no longer exists, stops being the proxy password.
- **Decisions per connection.** A connection is an `egress` action, decided by
  host plus the principal's session labels. An `ask` holds the connection open for
  up to 60 s waiting on the user (the UI shows the host), then refuses. The
  common case, a host already allowed, is a map lookup.
- **Taint-aware.** A host first contacted from a session with A gets `ask`
  (section 5), so a tainted run cannot reach a new exfiltration host unnoticed.
- **Core's own fetches.** `web_fetch` and search backends other than local SearXNG
  go through the proxy as the calling run's principal, so their URLs are decided
  and attributed like anything else. Today they leave Core directly.

## 11. Monitor, alerts and halt

The monitor consumes `/v1/observe` (turns, reasoning, tool calls, decisions)
asynchronously, off the critical path.

- **Deterministic tripwires.** Canary hits; any read of vault-like paths; repeated
  denied actions (5 in 10 min from one session); attempts to call a tool the agent
  was not given; a surrogate sent to an unbound host; egress volume spikes from one
  principal.
- **Model checks**, sampled and triggered rather than continuous, since local GPU
  time is scarce. A `decision` call runs on a session's recent trajectory when a
  tripwire fires, when ingest flagged injection, and every N tool calls of a
  long task (default 25). The questions are `pursuing_unrequested_goal`,
  `evading_oversight` and `fabricating_results`.
- **Reasoning review** (a setting, off by default). Bounded excerpts of reasoning
  go into these checks and into the reviewer. Excerpts are untrusted text, so the
  checks score them and never follow them.
- **Severities.**
  - Low: an Inbox entry.
  - Medium: an Inbox entry and a push.
  - High: halt.

### Halt

- Sentry sets `halted` (persisted). Every decide is denied, and the proxy refuses
  new connections and closes open ones.
- Core receives `halt.started`: it aborts all runs, pauses the scheduler, and
  shows a banner and a push.
- Only the user resumes, from the UI, after reading the alert. Resuming can revoke
  grants made in the halted session.
- The halt button is always visible in the UI. It is the same mechanism with
  reason "user".

## 12. Audit

`sentry.audit` is append-only: one row per decision, approval, grant change,
vault change, alert, halt and surrogate resolution. It stores the action and its
digest, never secret values. Retention is a setting, default 180 days. The UI shows
a filterable timeline and exports JSONL.

## 13. Performance budget

| Path | Budget | Measured by |
|---|---|---|
| `decide`, deterministic verdict, Unix socket round trip | p99 < 2 ms | `go test -bench` in `internal/sentry`; latency histogram in `/v1/metrics` |
| `ingest`, 8 KiB untrusted text, Guard on CPU | p50 < 60 ms | benchmark with the Guard container |
| reviewer | p50 < 1 s, cut-off 5 s | `model_calls`-style log in Sentry |
| proxy, allowed host | < 1 ms added to connect | benchmark |

Reviewer calls are expected on fewer than 10% of actions. The Activity view shows
the actual rate.

## 14. Packages

```
cmd/eika-sentry              wiring: store, vault, proxy, plugin host, API server
internal/sentry/api          wire types shared with Core (the only package Core imports)
internal/sentry/policy       hard rules, user rules, defaults, matching
internal/sentry/grant        grants and their cache
internal/sentry/approval     approvals, waits, expiry, grouping
internal/sentry/taint        session labels
internal/sentry/vault        entries, surrogates, secure entry, canaries
internal/sentry/scan         redaction, one-time codes, injection screen client, outbound tiers
internal/sentry/review       reviewer and monitor checks over systemone
internal/sentry/monitor      tripwires, alerts, halt
internal/sentry/audit        audit log
internal/sentry/pluginhost   MCP connections (reuses internal/mcp) and result filtering
internal/sentry/browser      relay to browserd
internal/sentry/store        schema sentry, migrations
internal/egress, netguard    moved here, imported by Sentry only
internal/systemone           System One client, shared
```

Boundary test (phase 0's import-rule test, extended): Core never imports
`internal/sentry/*` except `api`, and Sentry never imports `server`, `agent` or
`computer`.

## 15. Testing

- **Unit.** Every row of the defaults table; hard rules; rule matching; grant
  scopes; approval expiry and idempotency; redaction, one-time-code removal, Luhn
  and ID detectors; surrogate binding.
- **Docker tests.** Core and Sentry together. A scripted model tries to send to an
  unapproved recipient and the run parks; answering the approval resumes it.
  Restarting Core keeps the approval. A canary in a `bash` curl halts everything.
- **Injection evals** (`evals/sentry`, opt-in). Each scenario comes in three
  variants: innocent, injected and over-reach (Duet's method). Each is run against
  real local models, with recorded attack success, utility and approval rate.
  These suites are rerun whenever policy defaults change.
