# Interface: web app, notifications, channels

The web app is the main way to reach Eika, and it has to be good on a phone. Later
channels (Discord, Matrix, Telegram) bring the same home conversation into apps
the user already has. Approvals and secret entry never leave the web app.

## 1. Information architecture

```
Phone: bottom bar  [Chat] [Activity] [Inbox (badge)] [More]
Desktop: left rail  Chat, Activity, Inbox, Projects, Memory, Skills, Plugins,
                    Schedules, Browser, Vault, Settings          + Halt button
```

| Page | Contents |
|---|---|
| Chat | The one home session, the only place the user talks to Eika: a linear transcript of every channel's messages (each marked with its channel), composer, Eika's reasoning and tool calls, compaction cards, and compact subagent cards ("plumber · worker · running · Kitchen") that open the subagent's view |
| Activity | Eika's subagents (running, waiting with the reason, idle, failed), routine runs and dreams. Each opens a read-only live view of its session: streamed reasoning, tool calls and results, its report, approvals and questions, with Cancel. There is no composer: steering goes through Eika in Chat. The pool and queue strip shows what holds the GPU |
| Inbox | Approvals, questions from Eika, secret requests, elicitations, pending memories, alerts. Each is a card with its full context and actions (`sentry.md` 7). Badge counts unanswered items |
| Projects | List, then a project page: description, instructions editor, status memory, its subagents (watch, read-only), routines, files (a read-only listing of its directory) |
| Memory | `memory.md` 8, with the Instructions editor |
| Skills | Install (drop, zip, paste, git URL), read, enable, scope, assign to agents |
| Plugins | The MCP screens of today (connection, OAuth, tools, log), plus per-tool effect and verdict and the private and trusted flags |
| Schedules | `scheduler.md` 6 |
| Browser | Live view, takeover, tabs, site permissions, profile data, downloads |
| Vault | Secrets (names, kinds, bound domains, last used; never values), rotate, delete |
| Settings | Providers and models (kept), model classes, agents (definitions and overrides), Sentry grants, tool settings and the blocklist, channels, notifications, appearance, account, and system health (Docker, Sentry, Guard, endpoints with their probed capabilities) |
| Developer (in Settings) | Context inspector, model call log with token accounting and drift, Sentry audit timeline |

## 2. What carries over from today's frontend

Kept:

- `api/client.ts`, `api/connection.ts`, `api/stream.ts` (topics gain `inbox` and
  `activity`)
- TanStack Query over events, with no polling
- the transcript reducer (generalised beyond one open session, below)
- `Markdown`, the composer and `MarkdownField`, attachments and images
- `ToolCard` and the renderer registry
- the elicitation form (moved into Inbox cards)
- provider, model and MCP settings screens, connect and setup (minus the project
  step)
- `Notice`, `ConfirmDialog` and `ResizableSplit`
- the Context inspector
- the mock harness, the contract checking and `npm run shot`

Removed in phase 0:

- files, terminal, changes, sandbox, workspaces, git projects
- the workbench's projects, workspaces and sessions sidebar
- the panels that only those used, the session tree panel and the fork UI
- `lib/unifiedDiff`, `lineDiff`, `github`, `DiffRows`
- Monaco and xterm. The Memory and Skills editors use `MarkdownField` and a plain
  textarea for frontmatter-free files. Monaco is dropped as a dependency.

## 3. Mobile first

- **Shell.** `100dvh` with safe-area insets, a bottom bar under 640 px and a rail
  above. The composer sits on the `visualViewport`, so the keyboard never covers it.
- **Density.** The "Dense dev tool" tokens stay for desktop. A `touch` density
  under `(pointer: coarse)` raises the type scale one step, gives controls a
  44 px minimum hit area, and drops hover-only controls (Rewind becomes a row
  action). The style guide gains a short section on it. Tokens still define
  everything.
- **Composer.** On touch, Enter inserts a newline and a send button sends. The
  `chat` and `markdown` key modes stay for keyboards. The overlaid markdown field
  is tested on iOS Safari and Android Chrome with IME and autocorrect before phase
  3 closes. If it misaligns, touch uses a plain textarea with a rendered preview.
- **Long transcripts.** The home transcript loads the newest 50 entries and pages
  backwards on scroll (`runtime.md` 7). It virtualises rows beyond about 200. That
  needs one dependency, justified then, or about 150 lines of our own,
  windowing measured rows. Decide in phase 3 by measuring.
- **PWA.** A manifest, icons, a service worker that caches the shell (data is
  never cached offline) and installability, so it opens like an app from the home
  screen.

## 4. Notifications

- **In-app.** A toast, the Inbox badge and a notification list in Activity.
- **Web Push.** VAPID keys sealed in Core and a subscription per device, managed
  in Settings and Notifications. Push needs a secure context: `localhost`, or HTTPS
  through Tailscale serve or a reverse proxy. Push goes through the browser
  vendor's push service. The payload is end-to-end encrypted by the Web Push
  protocol and says only "Eika needs your approval" or "plumber reported"; details
  load when opened.
- **ntfy** (optional, self-hostable) for setups without HTTPS or for desktop
  notifications through ntfy's apps. The topic secret is sealed.
- **What notifies**, each set per category in Settings:
  - approvals: grouped into one push after a short delay (default 5 minutes),
    with the Inbox badge updating at once
  - secret requests and hand-offs: at once, with high urgency
  - alerts: by severity
  - subagent reports: when the subagent's `report` mode says so
  - routine results: per routine
- **Urgency** for subagent reports and routine results is judged by `decision` (or `fast`) with
  `needs_attention_now`, so a check-in at 2 pm with nothing new stays silent.

## 5. Remote access

The stack still binds 127.0.0.1. For a phone, the recommended setup is Tailscale.
`tailscale serve https / http://127.0.0.1:8080` gives HTTPS on the tailnet with no
open ports, and push works. `allowed_origins` and `public_url` already cover a
reverse proxy. The setup wizard gains a "Reach Eika from your phone" step that
detects the origin it is served on and explains the options. It never opens a port
itself.

## 6. Channels

A channel is an adapter that moves messages between an external chat app and the
home session (`runtime.md` 7):

```
type Channel interface {
  Name() string
  Run(ctx, deliver func(inbound Message)) error  // receive
  Send(ctx, out Message) error                   // reply or notify
}
```

The interface waits for its second implementation, per the style guide's rule on
abstractions. Web is the first, built into Core. Discord or Matrix is the second;
the user's Matrix-Bot project is a head start.

- Every channel feeds the one home session, as talking to the same person by text
  and in person is one relationship. Each message's `<context>` names its channel,
  replies go back where the message came from, and the web Chat shows all of it.
  Proactive messages go to the `notify_channel` setting.
- Only allowlisted user ids are accepted. Anything else is ignored and logged.
- Approvals, secret requests and takeovers are never done in a channel. The
  channel gets a link to the web Inbox instead, following Duet's rule that a
  sign-in form never arrives as a link, text or email.
- Channel messages are user input and trusted at the user's level, since the
  sender is allowlisted. Attachments from a channel are untrusted content.
- Deferred: voice (the user's Parakeet ASR and Chatterbox TTS servers are natural
  backends), and email to Eika's own address.

## 7. Testing

The mock harness stays the way UI work is checked. Its `World` gains these states:

- the home session, with compactions, and subagent sessions in every state
- the Inbox (approvals, questions, secret requests, alerts)
- memories, skills, routines
- the browser viewer, as a fake frame stream

The visual suite is rebaselined once at the new shell, with phone baselines for
every page (390x844) and a touch-density check. The composer spec is rerun on
touch emulation.
