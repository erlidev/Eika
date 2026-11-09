# Interface: web app, notifications, channels

The web app is the main way to reach Eika, and it has to be good on a phone. Later
channels (Discord, Matrix, Telegram) bring the same home conversation into apps
the user already has. Approvals and secret entry never leave the web app.

## 1. Information architecture

```
Phone: bottom bar  [Chat] [Activity] [Inbox (badge)] [More]
Desktop: left rail  Chat, Activity, Inbox, Projects, Memory, Skills, Plugins,
                    Schedules, Settings                          + Halt button
```

| Page | Contents |
|---|---|
| Chat | The one home session, the only place the user talks to Eika: a linear transcript of every channel's messages (each marked with its channel), composer, Eika's reasoning and tool calls, compaction cards, and compact subagent cards ("plumber · worker · running · Kitchen") that open the subagent's view |
| Activity | Eika's subagents (running, waiting with the reason, idle, failed) and routine runs. Each opens a read-only live view of its session: streamed reasoning, tool calls and results, its report and approvals, with Cancel. There is no composer: steering goes through Eika in Chat |
| Inbox | Approvals, elicitations and pending memories. Each is a card with its full context and actions (`sentry.md` 7). Badge counts unanswered items |
| Projects | List, then a project page: description, instructions editor, and its subagents (watch, read-only) and routines |
| Memory | `memory.md` 8, with the Instructions editor |
| Skills | Install from a zip, read, enable |
| Plugins | The MCP screens of today (connection, OAuth, tools, log), plus per-tool `allow`, `ask` or `block` settings |
| Schedules | `scheduler.md` 6 |
| Settings | Providers and models (kept), model classes, agents (class and prompt), grants and the blocklist, notifications, appearance, account, and system health (Docker, endpoints with their probed capabilities) |
| Developer (in Settings) | Context inspector, model call log with token accounting and drift |

## 2. What carries over from today's frontend

Kept:

- `api/client.ts`, `api/connection.ts`, `api/stream.ts` (topics gain `inbox` and
  `activity`)
- TanStack Query over events, with no polling
- the transcript reducer (generalised beyond one open session, below)
- `Markdown`, the composer and `MarkdownField`, attachments and images
- `ToolCard` and the renderer registry
- the elicitation form (moved into Inbox cards)
- provider, model and MCP settings screens, connect and setup
- `Notice`, `ConfirmDialog` and `ResizableSplit`
- the Context inspector
- the mock harness, the contract checking and `npm run shot`
- `lib/lineDiff` and `DiffRows`, which the prompt editor uses to show a custom
  prompt against the built-in one

The Memory and Skills editors use `MarkdownField` and a plain textarea for
frontmatter-free files.

## 3. Mobile first

- **Shell.** `100dvh` with safe-area insets, a bottom bar under 640 px and a rail
  above. The composer sits on the `visualViewport`, so the keyboard never covers it.
- **Density.** The "Dense dev tool" tokens stay for desktop. A `touch` density
  under `(pointer: coarse)` raises the type scale one step, gives controls a
  44 px minimum hit area, and drops hover-only controls (Rewind becomes a row
  action). The style guide gains a short section on it. Tokens still define
  everything.
- **Composer.** On touch, a plain textarea with a rendered preview, where Enter
  inserts a newline and a send button sends. The `chat` and `markdown` key modes and
  the overlaid markdown field stay for keyboards.
- **Long transcripts.** The home transcript loads the newest 50 entries and pages
  backwards on scroll (`runtime.md` 7). It virtualises only if a 5,000-entry session
  is measurably slow on a phone, with about 150 lines of our own or one dependency,
  justified then.
- **PWA.** A manifest, icons, a service worker that caches the shell (data is
  never cached offline) and installability, so it opens like an app from the home
  screen.

## 4. Notifications

- **In-app.** A toast, the Inbox badge and a notification list in Activity.
- **ntfy** for the phone: Core posts to a topic URL the user sets, on ntfy.sh or
  self-hosted, and the topic secret is sealed. It needs no HTTPS on Eika's side. The
  payload says only "Eika needs your approval" or "plumber reported", and details
  load when opened. Web Push is deferred (`DEFERRED.md`).
- **What notifies:** approvals, each at once; Eika's replies to background turns, so
  subagent reports and routine results; and halts.

## 5. Remote access

The stack still binds 127.0.0.1. For a phone, the recommended setup is Tailscale.
`tailscale serve https / http://127.0.0.1:8080` gives HTTPS on the tailnet with no
open ports. `allowed_origins` and `public_url` already cover a reverse proxy. The
README explains the options. Eika never opens a port itself.

## 6. Channels

Deferred (`DEFERRED.md`), first in line to return.

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
- the Inbox (approvals, elicitations, pending memories)
- memories, skills, routines

The visual suite is rebaselined once at the new shell, with phone baselines (390x844)
for Chat, Activity and Inbox and a touch-density check. The composer spec is rerun on
touch emulation.
