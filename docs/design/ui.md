# UI: the web app's design

This is the design of the web app that phase 3 builds and later phases fill in:
the stack, the look, the shell, every page and its components, and how an avatar
and live speech fit in. `interface.md` says what the app must do and why. This
document says what it looks like and how it is put together. Where the two
disagree on information architecture or behaviour, `interface.md` and `PLAN.md`
win; on look, layout and component design, this document wins.

Sections 1 to 10 are phase 3's target, with the pages of later phases drawn so the
shell does not have to change when they land. Sections 11 to 13 (presence, avatar,
speech) are not in `PLAN.md`. Only their seams are built in phase 3
(section 16 lists them). The rest waits in `DEFERRED.md` until moved into the plan.

## 1. Principles

1. **One conversation, one person.** The app is a messenger for one relationship,
   not a workbench. Chat is the home screen, and it is the only place with a
   composer. Every other page is for watching, answering or configuring.
2. **Content first, chrome quiet.** The transcript and the avatar are the two
   things worth looking at. Navigation, meters and controls stay at the edges,
   small, and in neutral colour until they need attention.
3. **Phone first, desktop roomy.** Each page is designed at 390 px and then
   given more columns, never the other way round. No control depends on hover.
4. **Eika's state is always visible.** One presence indicator says whether Eika is
   idle, listening, thinking, working or speaking, in every layout, with or without
   an avatar. The avatar is that indicator's richest form, not a separate feature.
5. **Text is the source of truth.** Speech and avatar are layers over the
   transcript. Everything said is in the transcript; everything an avatar expresses
   has a text equivalent. Turning both off loses nothing but delivery.
6. **Fast by subtraction.** Few dependencies, CSS before JavaScript, routes and
   heavy features loaded on demand, and nothing rendered that is off screen. The
   avatar never costs anything until it is turned on.
7. **Automatic, like the safety model.** The UI asks only what `PLAN.md` "What
   asks" lists. It does not add confirm dialogs to reversible actions.

## 2. Stack

The stack stays. React 19, Vite 8, strict TypeScript, Tailwind 4, shadcn over
Radix, TanStack Query, Zustand and React Router 7 are all current, and none is the
bottleneck: the 16 MiB `web/dist` of today is Monaco and the shell, which phase 0
removed. Changing framework would buy about 40 kB and cost the mock harness, the
contract checks and every kept component (`interface.md` 2).

| Concern | Choice | Notes |
|---|---|---|
| Framework | React 19.3 | `<ViewTransition>` is stable in 19.3 and animates layout changes (section 5). The React Compiler stays off until a measurement on the transcript says it pays |
| Build | Vite 8 (Rolldown), `@vitejs/plugin-react` | Routes and heavy features are separate chunks (section 14) |
| Styling | Tailwind 4, tokens in `index.css` | As today. The token set changes (section 3) |
| Primitives | shadcn on `radix-ui` | shadcn's new default is Base UI. Migrating 60 components buys nothing here, so Radix stays until it is deprecated |
| Sheets | Radix `Dialog` styled as a bottom sheet | Vaul is unmaintained. Sheets close by button, scrim tap or `Esc`; drag-to-dismiss is not built |
| Routing | React Router 7, data router, `lazy` routes | One route per page (section 4) |
| Server state | TanStack Query, invalidated by events | No polling, as today |
| Stream | The one WebSocket (`api/stream.ts`) | Topics gain `inbox`, and `activity` only if `session.updated` on `global` proves too coarse. Voice gets a second socket of its own (section 13) |
| Client state | Zustand, one store per feature | New stores: `presence`, `layout`, `voice`, `avatar` |
| Markdown | `react-markdown`, `remark-gfm`, `lowlight` | Kept, with block-level memoisation while streaming (section 14) |
| Lists | Paged | P3-16 kept the transcript without a virtualiser after mobile emulation; a physical phone was not measured |
| Animation | CSS only: transitions, `@starting-style`, view transitions | No animation library |
| Icons | lucide | As today |
| Fonts | Geist Variable, Geist Mono Variable (`@fontsource-variable`) | Replace IBM Plex Sans and JetBrains Mono (section 3). OFL, self-hosted |
| PWA | Hand-written service worker, about 50 lines | Network-first for the document, cache-first for hashed `/assets/`. Nothing under `/api` is ever cached |
| Lint, format | ESLint 10, Prettier | Kept: the token rules are custom ESLint rules |
| Tests | Vitest, Playwright on the mock harness | Kept. Phone baselines at 390x844 |
| Avatar | Official Cubism 5 SDK for Web, the engine from `live2d-lab` | No PixiJS. Loaded only when an avatar is on (section 12) |
| Audio | Web Audio: `AudioWorklet` capture, scheduled `AudioBufferSourceNode` playback | No library (section 13) |

New dependencies in phase 3: the two font packages, replacing two. Nothing else.

Rejected:

- **Preact, Solid or Svelte.** Smaller runtimes, but the runtime is not what is
  large here, and Radix, shadcn and the kept components are React.
- **TanStack Router.** Typed search params are its gain. The app has about four
  search params.
- **Streamdown.** 150 kB gzipped for what a block splitter of about 100 lines
  and the existing `Markdown` component do.
- **PixiJS with a Live2D plugin.** About 280 kB gzipped of renderer the lab's
  engine does not use, and the plugins hide the update order the engine depends
  on (`live2d-lab/RIG.md`, "Update order").
- **WebRTC for voice.** It needs UDP and usually a TURN relay. Eika is reached
  over `tailscale serve` or a reverse proxy, which carry HTTPS and WebSocket only.

## 3. Visual language

The direction changes from "Dense dev tool" to **Calm companion**: a quiet,
roomy messenger. Neutral surfaces with a faint cool tint, one accent (the teal
of today, kept so Eika still looks like Eika), soft corners, generous line
height, and depth from surface lightness instead of borders and shadows.
`STYLE_GUIDE.md` "Web UI design" is rewritten to match when phase 3 starts. Its
rules of method stay: tokens only, no palette colours, no arbitrary values, ESLint
enforcing it.

### Colour

Both themes are first class. Dark is the default for a new install, since an
avatar stage reads best on a dark ground; "System" and "Light" are in Appearance.

| Token | Light | Dark | Used for |
|---|---|---|---|
| `background` | `oklch(0.985 0.003 240)` | `oklch(0.165 0.012 255)` | The page |
| `surface` (`card`) | `oklch(1 0 0)` | `oklch(0.205 0.014 255)` | Cards, the composer, the rail |
| `raised` (`popover`) | `oklch(1 0 0)` | `oklch(0.245 0.016 255)` | Menus, sheets, dialogs, toasts |
| `muted` | `oklch(0.96 0.005 240)` | `oklch(0.235 0.015 255)` | The user's bubble, code blocks, quiet fills |
| `foreground` | `oklch(0.21 0.02 250)` | `oklch(0.95 0.008 240)` | Text |
| `muted-foreground` | `oklch(0.50 0.02 250)` | `oklch(0.70 0.02 250)` | Secondary text, icons at rest |
| `border` | `oklch(0.92 0.006 240)` | `oklch(0.29 0.018 255)` | Hairlines between regions, input outlines |
| `primary` | `oklch(0.54 0.10 210)` | `oklch(0.78 0.11 205)` | The one action on a screen, links, focus ring, presence |
| `accent` | `oklch(0.95 0.02 205)` | `oklch(0.29 0.04 220)` | Hover and selected rows |
| `success`, `warning`, `destructive` | as today | as today | State. `destructive` is also Halt |
| `stage-from`, `stage-to` | `oklch(0.95 0.02 215)`, `oklch(0.985 0.003 240)` | `oklch(0.24 0.035 230)`, `oklch(0.165 0.012 255)` | The gradient behind the avatar |

Rules:

- Elevation is lightness: `background` < `surface` < `raised`. A shadow appears
  only on `raised`. A border separates two regions of the same surface, and is
  not drawn around a card that already differs from its ground.
- `primary` appears at most once per screen as a filled control. Everything else
  that is interactive is neutral until hovered, focused or selected.
- Text contrast is at least 4.5:1 and non-text 3:1 in both themes. A Vitest test
  computes it from the token values, so a token edit cannot regress it.
- The shadcn names (`card`, `popover`) stay as the variables. `surface` and
  `raised` are aliases feature code uses.

### Type

Geist for everything a person reads or wrote, Geist Mono for everything a machine
produced or reads (the style guide's existing split). One family pair, two files.

| Step | Size / line | Used for |
|---|---|---|
| `text-2xs` | 11 / 16 | Badges, timestamps, key hints |
| `text-xs` | 12 / 16 | Metadata, status lines, chips |
| `text-sm` | 14 / 20 | UI text: rows, buttons, forms |
| `text-base` | 16 / 26 | Transcript prose, the composer, captions on a phone |
| `text-lg` | 18 / 28 | Page titles, card titles on Inbox |
| `text-2xl` | 24 / 32 | Standalone screens, live captions on desktop |

The scale is one step larger than today at every name. The touch density
(`(pointer: coarse)`) changes hit areas and spacing, not type, because 16 px prose
and 14 px UI text are already phone sizes; this replaces "raises the type scale
one step" in `interface.md` 3. Weights: 400 body, 500 labels and buttons, 600
headings. Prose measure is capped at 72 characters (`max-w-[46rem]` as a token,
`--measure`).

Geist covers Latin, and transcripts will hold Japanese. The stack is
`"Geist Variable", "Hiragino Sans", "Yu Gothic UI", "Noto Sans JP", system-ui,
sans-serif`: CJK falls to the system face, and no CJK font is shipped. The style
guide's "only what the bundled fonts cover" rule keeps applying to UI strings,
not to conversation content.

### Shape, space, motion

- **Radius.** Three tokens replace the single one: `--radius-control` 8 px
  (buttons, inputs, chips, rows), `--radius-surface` 14 px (cards, sheets,
  dialogs, bubbles, tool cards), and `rounded-full` (pills, dots, the composer's
  send button, the presence mark). Tailwind's steps map onto them: `sm` and `md`
  to control, `lg` and up to surface.
- **Spacing.** `--spacing` returns to Tailwind's 0.25 rem. Regions are separated
  by space first and a hairline second.
- **Touch density.** Under `(pointer: coarse)`: 44 px minimum hit area on every
  control, 12 px minimum gap between adjacent targets, row height 52 px, and
  hover-only controls become a row menu.
- **Motion.** Three durations: 120 ms (hover, press), 200 ms (enter, exit,
  disclosure), 320 ms (layout: a sheet, a route, the avatar changing slot). One
  easing, `cubic-bezier(0.2, 0, 0, 1)`. Under `prefers-reduced-motion`, layout
  motion becomes a cross-fade and the avatar holds a still pose (section 15).
- **Streaming text** does not animate per token. A new block fades in over
  120 ms; text inside the growing block just appears.

## 4. Shell and navigation

```
Desktop (>= 1024)                               Phone (< 640)
┌────┬──────────────────────────────────────┐   ┌────────────────────────┐
│ E  │ Chat          ● Eika · idle   ◔  ▣ ⋯ │   │ ● Eika · idle   ◔ ▣ ⏻ │  header 48
│    ├──────────────────────────────────────┤   ├────────────────────────┤
│ ▢  │                                      │   │                        │
│ ▢  │            page content              │   │      page content      │
│ ▢3 │                                      │   │                        │
│ ▢  │                                      │   │                        │
│ ▢  │                                      │   ├────────────────────────┤
│ ─  │                                      │   │ composer (Chat only)   │
│ ⏻  │                                      │   ├────────────────────────┤
│ ⚙  │                                      │   │ Chat  Activity Inbox³ ⋯│  bar 56 + inset
└────┴──────────────────────────────────────┘   └────────────────────────┘
 rail 64
```

- **Rail** (640 px and up): 64 px wide on `surface`, an icon with a `text-2xs`
  label under it per page. Order: Chat, Activity, Inbox, Projects, Memory,
  Schedules, Plugins, Skills. Pinned to the bottom: Halt, then Settings. Pages a
  phase has not built yet are absent, not disabled. The selected page has an
  `accent` pill behind its icon.
- **Bottom bar** (under 640 px): Chat, Activity, Inbox, More. More opens a sheet
  with the remaining pages and Settings. The bar hides while the on-screen
  keyboard is up and in the Stage layout.
- **Header** (48 px, every page): the page title on the left; page actions on
  the right. On Chat the title is the presence chip (section 11). On a phone the
  header also carries Halt, since the bar has no room for it.
- **Badges.** Inbox shows the count of unanswered items. Activity shows a dot
  while anything is running. Nothing else badges.
- **Halt** is a `destructive`-outlined button, always visible, one press, no
  confirmation (`PLAN.md` phase 4). While halted, a `destructive` banner sits
  under the header on every page: "Eika is halted" and a Resume button.
- **Banners** stack under the header in this order: halted, connection lost
  ("Reconnecting…", with the composer disabled after 5 s), guard not answering
  (`unscanned`). At most two show; the rest fold into "and 1 more".
- **Toasts** appear top-centre on a phone and bottom-right on desktop, one at a
  time, 5 s, each a link to its page. An approval's toast stays until answered or
  dismissed.
- **Command palette** (`Cmd/Ctrl+K`, desktop): go to a page, open a project or a
  subagent, switch layout, toggle theme, halt. `cmdk` is kept.
- **Viewport.** `100dvh`, `viewport-fit=cover`, safe-area insets on the header,
  bar and composer, and `interactive-widget=resizes-content`. Where that meta is
  not honoured (Safari), a `visualViewport` listener sets `--keyboard-inset` and
  the composer sits on it.

### Routes

| Route | Page | Chunk |
|---|---|---|
| `/` | Chat | initial |
| `/live` | Chat in the Stage layout (section 5) | initial, plus avatar and voice on demand |
| `/activity`, `/activity/:sessionId` | Activity and a subagent's view | lazy |
| `/inbox`, `/inbox/:itemId` | Inbox | lazy |
| `/projects`, `/projects/:slug` | Projects | lazy |
| `/memory`, `/memory/:id`, `/memory/instructions` | Memory | lazy |
| `/schedules`, `/schedules/:id` | Schedules | lazy |
| `/plugins`, `/plugins/:id`, `/skills`, `/skills/:name` | Plugins, Skills | lazy |
| `/settings/:section?` | Settings, a page and no longer a dialog | lazy |
| `/mcp/callback` | OAuth return | lazy |

Every list-and-detail page uses one pattern, `MasterDetail`: two panes on
desktop with the list at 320 px, and on a phone the list, then the detail as a
pushed route with a back button. Deep links work at both sizes because the detail
is always a route.

### Folders

```
app/          Shell, Rail, BottomBar, Header, banners, routes, CommandPalette, theme
features/
  chat/       the Chat page: layouts, composer, needs-you tray
  session/    Transcript and its parts, shared by Chat and Activity; the reducer
  presence/   the presence store, PresenceChip, PresenceMark
  activity/   inbox/   projects/   memory/   schedules/   plugins/   skills/
  settings/   providers/   setup/   connect/   notifications/
  avatar/     stage, slots, engine port, packs          (section 12)
  voice/      capture, playback, live session, captions (section 13)
components/   MasterDetail, Sheet, Notice, Screen, Markdown, ...; ui/ is shadcn
api/  lib/    as today
```

`app/panels.tsx` and the workbench go. `EXTENDING.md` "Adding a UI panel"
becomes "Adding a page": a route in `app/routes.tsx` and an entry in the rail's
list.

## 5. Chat

Chat has three layouts. They are the same page, the same transcript store and the
same composer; only where the avatar sits changes. The layout is a persisted
preference, switched from the header (the `▣` control) or by route.

```
Focus (default)            Companion (desktop)              Stage (/live)
┌──────────────────┐   ┌──────────────┬──────────┐   ┌──────────────────────┐
│                  │   │              │          │   │                      │
│    transcript    │   │  transcript  │  avatar  │   │        avatar        │
│    (46rem col)   │   │              │  (bust)  │   │      (full body      │
│                  │   │              │          │   │       or bust)       │
│                  │   │              │ ● state  │   │                      │
├──────────────────┤   ├──────────────┤          │   │   captions, 2 lines  │
│     composer     │   │   composer   │          │   │  ◉ mic   ⌨   ▤   ✕  │
└──────────────────┘   └──────────────┴──────────┘   └──────────────────────┘

Companion (phone)                       Stage (phone)
┌────────────────────┐                  ┌────────────────────┐
│   avatar (bust)    │ 36dvh, shrinks   │                    │
│                    │ to a 56 px puck  │       avatar       │
├────────────────────┤ when the user    │                    │
│    transcript      │ scrolls back     │                    │
├────────────────────┤                  │  captions, 3 lines │
│     composer       │                  │   ◉    ⌨    ▤   ✕  │
└────────────────────┘                  └────────────────────┘
```

- **Focus.** Text only. Presence is the chip in the header. This is the layout
  when no avatar is installed, and it is what phase 3 ships.
- **Companion.** The avatar beside the conversation. On desktop it is a right
  column, 360 px by default, resized with `ResizableSplit` between 280 and 520 px.
  On a phone it is a strip above the transcript that collapses to a puck in the
  header's corner when the user scrolls up, and expands on tap.
- **Stage.** The avatar fills the page for a live conversation. The transcript
  becomes captions, and the full transcript is one tap away as a sheet (`▤`).
  The keyboard button (`⌨`) opens the composer over the captions for a typed
  message. Leaving (`✕`, `Esc`, or the back button) returns to the layout the
  user came from.

The layouts change with a view transition: the avatar keeps its
`view-transition-name`, so it glides and scales between its slots.

### Transcript

One column, capped at `--measure`, centred. It is `features/session`'s
`Transcript`, the same component Activity uses without a composer.

| Part | Design |
|---|---|
| Day separator | A centred `text-xs` date between entries of different days |
| User message | Right-aligned bubble on `muted`, `--radius-surface`, at most 85% of the column. Images above the text. A channel chip under it when it did not come from the web app ("Discord", "Voice") |
| Eika's reply | No bubble: prose on the page ground, full column width, flush left. This reads as a document, keeps code blocks and tables wide, and makes the two speakers distinct without colour |
| Work trail | A turn's reasoning and tool calls fold into one line above the reply: "Worked for 42 s · 5 steps". While the turn runs it is open and shows the current step live ("Reading pkg.go.dev…"); when the reply starts it closes. Opening it lists reasoning blocks and `ToolCard`s in order |
| `ToolCard` | The renderer registry is kept. A card is a row (icon, tool name in mono, one-line summary, duration, state) that expands to arguments and result. Marks: `withheld` (the screen removed part), `unscanned`, `blocked` |
| Subagent card | A row on `surface`: state dot, name, agent, project, elapsed ("plumber · worker · running · Kitchen"). It opens `/activity/:sessionId`, as a side sheet on desktop so Chat stays in view |
| Notice | A centred, muted line with an icon: a report arrived, Eika restarted, a memory changed. A report's notice links to its subagent |
| Compaction card | A hairline divider with a label, "Context compacted · 84k → 12k tokens", that expands to the summary, the method and the timings |
| Background turn | A reply Eika started on its own carries a small "while you were away" label on its first block |
| Remote image | Loaded only when its URL's source allows (`PLAN.md` phase 4b); otherwise a link |

Behaviour:

- **Paging.** The newest 50 entries load first. Scrolling within 600 px of the
  top fetches the previous page, and the scroll position is held by anchoring to
  the first visible entry. Entries further than two screens from view get
  `content-visibility: auto` with a remembered intrinsic size.
- **Following.** The view sticks to the bottom while the user is at the bottom.
  Scrolling up releases it and shows a "Jump to latest" pill above the composer,
  with a dot when something arrived. A hook of about 80 lines in
  `features/session`, with tests; no dependency.
- **Row actions.** Copy, Speak (section 13), and Rewind where the entry allows
  it. On desktop they appear in a small bar on hover and on focus; on touch,
  behind a `⋯` button at the end of each message, never on long-press alone.
- **Empty state.** A new install shows the presence mark (or the avatar), one
  line of greeting, and three suggestion chips that fill the composer.
- **Screen readers.** The live-region rule of today stays: a finished reply is
  announced once, never per token.

### Needs-you tray

`PLAN.md` phase 11 lets a call that asks wait two minutes when the user is at the
UI. An approval that arrives while Chat is open therefore appears in a tray
directly above the composer, as the same card component the Inbox uses, collapsed
to its title and first answer with "Details" to expand. Elicitations appear the
same way. The tray holds at most one card open and a count for the rest
("2 more in Inbox"). Answering there answers the Inbox item.

### Composer

```
┌──────────────────────────────────────────────────────────────┐
│ [img ✕] [img ✕]                                              │
│ Message Eika                                                 │
│                                                              │
│ ＋   ◔ 38%                                     🎙      ( ↑ ) │
└──────────────────────────────────────────────────────────────┘
```

- A single `surface` box with `--radius-surface`, no outline until focused. The
  field grows with its content up to 40% of the viewport (`field-sizing:
  content`, with a scroll-height fallback), then scrolls.
- **Attach** (`＋`): images by picker, paste or drop; on a phone the picker
  offers the camera. Thumbnails sit above the text with a remove button each.
- **Context meter** (`◔`): a ring with the fill of Eika's window. It opens the
  context breakdown. On a phone it lives in the header instead.
- **Mic**: tap to dictate into the field; hold to talk and send on release
  (section 13). Hidden until a speech backend is configured.
- **Send** is the one filled `primary` control on the page. While a run is in
  progress and the field is empty it becomes Stop.
- **While Eika is working**, a message can still be sent. The send button gains a
  two-way choice above it, "Now" (steer) and "After" (follow-up), defaulting to
  Now. Queued messages show as chips above the field, each removable.
- **Keys.** With a keyboard: the `chat` and `markdown` key modes and the overlaid
  `MarkdownField` stay, `Esc` aborts as today. On touch: a plain textarea, Enter
  is a newline, the button sends, and a Preview toggle renders the draft.
- The draft, its attachments and the chosen mode persist per device.

## 6. Activity

`MasterDetail`. The list has two tabs: **Agents** (subagents now; routine runs and
wakeups join in phase 7) and **Recent** (the in-app notification list, deferred
with it in `DEFERRED.md`; until it returns the list has no tabs).

- **Agents list.** Groups in this order, empty groups hidden: Waiting, Running,
  Idle, Failed and cancelled. A row: state dot, name, agent and project in
  `muted-foreground`, elapsed time, and under it one live line of what it is
  doing now (the last tool call, or the waiting reason in `warning`). Filter
  chips for project appear when there is more than one.
- **Detail.** A header with the name, state, project and Cancel. Below it the
  brief, collapsed; then `Transcript`, live, with no composer; then the report as
  a card once there is one. A line under the header says "To steer this, tell
  Eika in Chat" with a link.
- **Recent.** Newest first: reports, background replies, halts, each a link.
  Opening the tab marks them read.

State colours: running `primary`, waiting `warning`, idle `muted-foreground`,
failed `destructive`, done `success`. The same dot is used on subagent cards in
Chat and on the rail's Activity badge.

## 7. Inbox

Arrives in phase 5 with pending memories and gains approvals, tripwire entries and
elicitations as their phases land. One column of cards capped at 40 rem, newest
first, with filter chips (All, Approvals, Security, Memory, Forms) once more than
one kind exists.

```
┌────────────────────────────────────────────────────┐
│ ✉ Send mail                          Gmail · 2 min │
│ Eika wants to send a message to someone new.       │
│                                                    │
│ to       anna@example.org                          │
│          first seen: a web page (example.org/team) │
│ subject  Quote for the kitchen                     │
│ body     Hi Anna, …                     Show all ▾ │
│                                                    │
│ [ Always for this destination ]   [ Once ]   ⋯     │
│                                            Deny    │
└────────────────────────────────────────────────────┘
```

- Every card has the same anatomy: what and where from, one sentence in plain
  words, the full facts (arguments in mono, never truncated without "Show all"),
  then the answers. The plan's first answer is the filled button; rarer answers
  ("Always for this tool", "Always when…") are under `⋯`; Deny is plain text.
- Asks from one run share a card with a row per call.
- A tripwire card shows the chain as a short vertical timeline and the two
  answers "This was fine" and "Stop the run".
- A pending memory shows its text with the flagged span marked, and Accept, Edit,
  Reject.
- An answered card collapses to one line with its outcome and leaves the list on
  the next visit. Expired cards say so.
- Empty state: "Nothing needs you."

## 8. Projects, Memory, Schedules, Plugins, Skills

All five are `MasterDetail` pages built from the same parts: a searchable list,
a detail with `SectionTabs`, `MarkdownField` for anything edited as Markdown, and
a sticky save bar that appears only when there are unsaved changes.

| Page | List row | Detail |
|---|---|---|
| Projects | Name, status chip, one line of description, a dot when a subagent is running | Tabs: Overview (description, directory in mono, status), Instructions (editor), Subagents (Activity rows, watch only), Routines (phase 7) |
| Memory | Title, type chip, pinned mark, pending marker | The editor: frontmatter fields as a form, the body as `MarkdownField`; Pin, Archive, Purge. A second top-level tab, Instructions, holds "About me" and the rules. Filters: type, state, pinned. Search is FTS as the user types |
| Schedules | Name, the schedule in words ("Weekdays at 08:00"), next run, an enable switch, last outcome dot | The prompt, the schedule with its next five times, run history linking to each run's session. A second tab lists wakeups |
| Plugins | Name, state badge (`connected`, `unauthorized`, …) | Tabs: Connection (today's MCP form and OAuth), Tools (one row per tool with a four-way segmented control: allow, known, ask, block), Grants (known destinations, revocable), Log |
| Skills | Name, enabled switch, warning mark | The rendered `SKILL.md` with the screen's hits marked, the file list, Enable. Install is a drop zone at the top of the list |

## 9. Settings

A page with its own section list: a left column on desktop, a list that pushes to
each section on a phone.

| Section | Contents |
|---|---|
| Models | Providers and models (kept screens), model classes |
| Agents | Each agent's class and prompt, with `DiffRows` against the built-in prompt |
| Computer | Limits and egress |
| Notifications | ntfy topic and test button, what notifies, in-app toasts |
| Appearance | Theme, layout default, reduced motion override, text size (three steps, scaling `html` font size) |
| Avatar and voice | Section 12 and 13's settings. Shown once either feature exists |
| Security | Guard health, thresholds, blocklist, trusted sources, withheld list, tripwires, journal (phases 4 and 11) |
| Account | Password, sign out, devices |
| System | Docker, endpoints and their probed capabilities, the inference queue |
| Developer | Context inspector, model calls with drift |

Settings rows keep today's rule: the name and one line of detail truncate, and
actions wrap below on a narrow screen. A setting saves when changed; only prose
editors have a save bar.

## 10. Entry screens and PWA

- **Sign-in, setup, load failure** keep `Screen`, restyled by the tokens. The
  setup wizard's steps stay.
- **Install.** The manifest sets `display: standalone`, theme colours for both
  schemes, and maskable icons. After the first successful chat on a phone
  browser, a dismissible `Notice` in More explains "Add to Home Screen". There is
  no install prompt on load.
- **Service worker.** Caches the document and hashed assets so the app opens
  instantly and shows "Reconnecting…" when the server is unreachable. It never
  caches `/api`. A new version activates on the next launch; a toast offers
  "Reload" if the app is open when one is waiting.
- **Opening from a push.** An ntfy notification opens `/` for a reply or report,
  and `/inbox/:itemId` for an approval.

## 11. Presence

Presence is Eika's state as the UI shows it. It is one Zustand store,
`features/presence`, derived from events the app already receives, and every
visual form of Eika reads it. Building it first is what lets the avatar and voice
arrive later without touching Chat.

| State | Entered on | Text |
|---|---|---|
| `offline` | The socket is down | "Reconnecting…" |
| `halted` | Halt | "Halted" |
| `idle` | `turn.end`, and by default | "Eika" |
| `listening` | The mic is open and the user is speaking (section 13) | "Listening" |
| `thinking` | `turn.start`, `reasoning.delta` | "Thinking" |
| `working` | `tool.call`, until its result | "Working · web_fetch", the tool's verb |
| `replying` | `message.delta` with no audio | "Replying" |
| `speaking` | Speech audio is audible | "Speaking" |

The store also holds `mood` (the face state the last cue named, section 12),
which only an avatar renders.

Three renderers, in rising cost:

1. **`PresenceChip`**, always present in Chat's header: a dot and the text above.
   This is the accessible form and the only one screen readers get.
2. **`PresenceMark`**, a 2D mark drawn in CSS: a soft ring in `primary` that
   breathes when idle, widens with the mic level when listening, orbits when
   thinking or working, and pulses with the speech envelope when speaking. It is
   the avatar's stand-in in the Companion and Stage layouts when no avatar is
   installed, loading or failed, and under reduced motion. It costs no JavaScript
   per frame except the audio level, which is a CSS variable.
3. **The avatar** (section 12).

Phase 3 ships the store, the chip, and the mark in the empty state.

## 12. Avatar

The avatar is a Live2D model on the official Cubism 5 SDK for Web, driven by the
procedural engine developed in `live2d-lab` (`src/engine`: idle, gaze, blink,
breath, faces, gestures, arms, lip sync, the phrase director). This section
says how that engine sits in the app. The engine's own design is the lab's
`RIG.md` and `research/`.

### Structure

```
features/avatar/
  AvatarHost.tsx     the one canvas, fixed in the shell, positioned over the active slot
  AvatarSlot.tsx     a placeholder a layout renders where the avatar should appear
  port.ts            AvatarPort: the message interface to the engine
  engine/            the lab's engine and Cubism wrapper, framework-free TypeScript
  packs.ts           installed packs, loading, quality tiers
```

- **One canvas, many slots.** `AvatarHost` owns the only `<canvas>` and WebGL
  context for the life of the page. Layouts render an empty `AvatarSlot`; the
  host measures the active slot (`ResizeObserver`) and moves and scales the
  canvas over it with a transform. Changing layout or route never recreates the
  context or reloads the model, and the move is a view transition.
- **A message boundary.** React talks to the engine only through `AvatarPort`:
  `load(pack, tier)`, `resize(rect, dpr)`, `setCamera(name)`, `setMode(presence)`,
  `setMood(state)`, `cue(cue)`, `speak(segment)`, `clock(sync)`, `cancel()`,
  `dispose()`, and events back (`ready`, `error`, `contextlost`, `stats`). Every
  argument is structured-cloneable. The engine runs on the main thread first.
  Because nothing crosses the boundary but messages, it can move to a worker with
  an `OffscreenCanvas` if profiling shows the transcript and the avatar fighting
  for frames. Safari's WebGL-in-worker support decides whether that is worth it.
- **Driven by presence.** The host subscribes to the presence store and calls
  `setMode` and `setMood`. Chat does not know an avatar exists.
- **Lazy.** The engine, the Cubism framework and Core are one dynamic chunk,
  requested when a layout first mounts an `AvatarSlot` with an avatar enabled.
  Until `ready`, the slot shows the pack's poster image under the
  `PresenceMark`. On `error` it stays on the mark and a `Notice` in Avatar
  settings says why.

### Avatar packs

A model is installed, never bundled: models and Cubism Core have licences that
do not allow shipping them in this repository (the lab's model is "not cleared
for shipping").

- A **pack** is a zip the user installs in Settings: the model's files
  (`model3.json`, `moc3`, textures, `physics3`, `cdi3`, motions, expressions)
  plus `eika.avatar.json`, the rig manifest.
- **The manifest** is what `rig.ts` hard-codes in the lab, as data: the
  controller parameters, face states with their parameter poses, the viseme
  table, gestures (in and out motions, swap timing), props and outfit switches,
  cameras (`bust`, `half`, `full`, each a centre and scale), render requirements
  (mask buffer count and size), and credits and licence text. A model with no
  manifest gets a generic one from standard parameter ids (`ParamAngleX`,
  `ParamMouthOpenY`, …): it blinks, breathes, looks and opens its mouth, with no
  faces or gestures.
- **Core** is not in the repository either. Setup tells the user to supply
  `live2dcubismcore.min.js` from Live2D's SDK download, accepting its licence,
  and stores it beside the packs.
- Core serves packs under an authenticated, content-hashed path with immutable
  caching, so a 16 MB `moc3` downloads once per device.
- Credits from the manifest show in Avatar settings, as model licences require.

### Framing and quality

| Slot | Camera | Notes |
|---|---|---|
| Companion, desktop | `bust` | Head and shoulders; the face carries the acting at this size |
| Companion, phone | `bust`, tighter | The puck shows the face only |
| Stage | `half` on a phone, `full` on desktop | A double tap or `F` cycles cameras |

Quality is a tier chosen at load and adjusted from `stats`:

| | High | Balanced (default) | Low |
|---|---|---|---|
| Pixel ratio cap | 2 | 1.5 | 1 |
| Frame rate | display | 60, 30 when idle | 30 |
| Textures | as authored | at most 2048 | at most 2048 |
| Mask buffer | 1024 | 1024 | 512 |

- A 4096 px atlas is 64 MB of GPU memory, and the lab's model has five. Core
  writes 2048 px copies when a pack is installed, and phones load those.
- The loop stops when the page is hidden, the slot is off screen, or the layout
  is Focus. When the frame time stays over budget for three seconds the tier
  drops one step, with no prompt.
- `webglcontextlost` is handled: the host shows the poster, and rebuilds on
  `webglcontextrestored`.
- The lab's rendering requirements carry over as engine behaviour, including the
  end-of-frame `gl.flush()` that hides ANGLE's OpenGL fault.
- Battery saver, where the browser reports it, and `prefers-reduced-motion`
  force Low and a held pose with blinks only.

### Performance cues without speech

An avatar is useful before voice exists. With text only:

- The harness parses performance cues out of Eika's reply
  (`live2d-lab/research/03`, section 7) and sends them as events beside the
  text, so the app never sees or strips tags itself.
- Each cue is placed at a character offset in the message. The app fires it when
  the streamed text reaches that offset: a face state changes as its sentence
  appears.
- The phrase director runs on the text's punctuation with a synthetic clock, so
  the avatar nods and looks away while Eika "types", and the mouth stays shut.
- `thinking` and `working` map to the rig's thinking pose and, where the manifest
  has one, a prop such as the lab's loading spinner. A long stall plays the
  rig's stall bit.

## 13. Speech

Speech arrives in three steps, each useful alone and each a superset of the last.
All three treat voice as a **channel** in `interface.md`'s sense: a spoken turn is
an ordinary entry in the home session with the channel `voice`, shown in the
transcript with a chip. Nothing about a spoken conversation lives outside the
transcript.

| Step | What the user gets | Needs |
|---|---|---|
| 1. Read aloud | A Speak action on any reply, and a "Speak replies" toggle. The avatar lip-syncs | TTS |
| 2. Dictation and push-to-talk | The composer's mic: tap to dictate into the field, hold to talk and send | STT |
| 3. Live | Hands-free conversation in the Stage layout, with interruption | Both, streaming, plus turn detection |

### Transport

- A second WebSocket, `/api/voice`, opened only while speech is in use. Binary
  frames carry audio: 16 kHz mono PCM16 up in 20 ms frames, and the TTS engine's
  native rate down. JSON frames carry control and timing.
- The session's ordinary events still arrive on the main socket. The voice socket
  adds only what audio needs: partial transcripts of the user's speech, turn
  boundaries, and speech segments.
- A **speech segment** is one sentence or clause of a reply: its id and sequence,
  the text as spoken (the speech lane's text, without code or tables), its cues
  with offsets, phoneme timings when the TTS provides them, and then its audio.
- PCM is chosen because the link is a LAN or a tailnet and it needs no codec on
  either side. Opus through WebCodecs is the upgrade if a metered link ever
  matters.

### Playback and the clock

- One `AudioContext`, created on the user's first Speak, mic or Live tap (the
  gesture browsers require), at the TTS sample rate.
- Segments are scheduled back to back as `AudioBufferSourceNode`s behind a
  150 ms jitter buffer. Each one's start time on the audio clock is recorded.
- **Heard time** is the audio clock minus output latency
  (`getOutputTimestamp()`), plus a user offset. Everything timed to speech reads
  it: the avatar's mouth and cues, the caption highlight, and the `speaking`
  presence state, which starts when sound is audible and not when bytes arrive.
- Each segment's PCM is analysed when it arrives, before it is heard (the lab's
  `audio.ts`: envelope, pitch, accents, pauses). The avatar therefore animates
  with look-ahead, and the mouth can lead the sound slightly, as it should.
- Lip sync uses the lab's tiers: phoneme timings blended with energy when the
  TTS gives timings, spectral shaping when it does not, and energy alone as the
  floor.
- **Cancel** stops every scheduled node within one frame, tells the engine to
  close the mouth and return to `listening`, and tells the server how much of
  the reply was heard so the transcript marks the rest as unspoken.

### Capture

- `getUserMedia` with echo cancellation, noise suppression and auto gain, checked
  against `getSettings()`. An `AudioWorklet` resamples to 16 kHz and posts frames.
- The mic level drives the `PresenceMark` and the avatar's `listening` mode.
- Turn detection is the server's job. The client sends audio and renders what the
  server decides, so there is one opinion about when the user finished.
- For interruption only, the client also runs a local voice detector, so playback
  ducks within 100 ms of the user speaking and stops when the server confirms
  speech. An energy gate ships first; Silero through `@ricky0123/vad-web` (about
  2 MB, lazy) replaces it if the gate false-triggers.
- Echo is the known risk: on a phone's speaker, Eika can hear herself. If the
  server reports that a "user" turn matched the audio being played, the session
  drops to push-to-talk and says so once, suggesting headphones.

### Live mode

`/live` is the Stage layout plus an open voice session.

```
        ┌──────────────────────────────┐
        │ ● Listening            ◔  ⋯  │   presence chip, context, menu
        │                              │
        │           avatar             │
        │                              │
        │  so the quote covers the     │   captions: Eika's words, the word
        │  counters but not the sink   │   being heard at full contrast
        │                              │
        │   ( 🎙 )    ⌨     ▤     ✕    │   mute · type · transcript · end
        └──────────────────────────────┘
```

- **Entering.** The first time, a sheet explains what Live does and asks for the
  mic; the browser's prompt follows the user's tap on "Start". Entering holds a
  screen wake lock and sets Media Session metadata.
- **States** are presence states: `listening`, `thinking`, `working`,
  `speaking`. The user's own words appear as a muted partial caption while they
  speak, then become their transcript entry.
- **Controls.** Mute is the large control: tap to mute, and in push-to-talk mode
  hold it (or `Space`) to talk. Tapping the avatar or speaking interrupts Eika.
  `M` mutes, `Esc` ends. A menu holds input mode (hands-free or push-to-talk),
  captions on or off, the microphone and output device, and the audio offset.
- **Captions** are on by default. They show the spoken text. The transcript
  sheet shows the full reply, including whatever the speech lane left unspoken,
  marked "not read aloud".
- **Work in a live turn.** Tool calls show as one line above the captions
  ("Reading the page…"), and the avatar goes to its working pose. A subagent's
  report arriving mid-conversation is spoken as any background reply is.
- **Asks.** An approval is never answered by voice. Its card appears as a toast
  over the stage, Eika says that something needs a tap, and the conversation
  carries on.
- **Leaving.** Ending, navigating away, or the page going to the background
  closes the mic and the voice socket. iOS suspends audio in the background, so
  Live is a foreground mode everywhere, and returning shows "Live paused" with a
  Resume button. Nothing is lost: the conversation was the home session all
  along.
- **Without an avatar**, the stage shows the `PresenceMark` at full size. Live
  works the same.

### Settings (Avatar and voice)

Presence (chip only, mark, avatar); installed packs with credits; default camera
and quality tier; speak replies (off, in Live only, always); voice; input mode;
captions; microphone and output device; audio offset (a slider, ±250 ms, with a
test phrase that claps in time). The STT and TTS endpoints are server settings in
the same section.

## 14. Performance

Budgets, checked in `make check` by a size test on the built chunks and a
Playwright trace with 4x CPU throttling at 390 px:

| Measure | Budget |
|---|---|
| Initial JavaScript (shell, Chat, Markdown) | 170 kB gzipped |
| Initial CSS | 25 kB gzipped |
| Fonts | 2 files, preloaded, `font-display: swap` |
| Any lazy route | 60 kB gzipped |
| Avatar chunk (engine, framework, without Core and model) | 150 kB gzipped, never in the initial graph |
| Opening Chat from the cached shell on a phone | Transcript visible in 1 s on a LAN |
| Streaming a reply | No long task over 50 ms; one render per frame at most |
| Scrolling a 5,000-entry session | Target: 60 fps on the reference phone. P3-16 measured mobile emulation and kept the list without a virtualiser |
| Avatar on with a reply streaming | 60 fps on desktop, 30 on a phone, no dropped audio |

How:

- **Deltas are batched.** The transcript store appends `message.delta` text to a
  buffer and commits once per animation frame, so a fast model costs one render
  per frame, not one per token.
- **Markdown is parsed per block.** A splitter in `lib/` cuts the reply at blank
  lines outside fences. Finished blocks are memoised by content; only the last,
  growing block is re-parsed, with an unclosed fence or emphasis closed for
  display. Highlighting runs on a finished code block, and a growing one is plain.
- **Subscriptions are narrow.** A message row subscribes to its own entry. The
  streaming reply is the only component that re-renders while a reply streams.
- **Routes are lazy**, and Settings' sections are lazy within it. The rail
  prefetches a route's chunk on pointer-down.
- **The avatar is isolated.** It renders outside React, reads presence through a
  store subscription, and stops when unseen (section 12).
- **Images** in the transcript are lazy, decoded off the main thread, with
  intrinsic sizes from the wire so nothing shifts.

## 15. Accessibility

- Every control is a `button`, `a` or labelled input, and every page works from
  the keyboard, as the style guide requires. The rail and bottom bar are `nav`
  landmarks; the transcript is a `log`.
- The avatar's canvas is `aria-hidden`. The `PresenceChip` beside it is the
  accessible statement of the same state, in a polite live region that announces
  state changes at most once every five seconds.
- Captions make Live usable without sound, and the composer makes it usable
  without a microphone.
- `prefers-reduced-motion`: no layout motion, no `PresenceMark` animation beyond
  a state colour, and the avatar holds a still pose with blinks. Appearance has
  an override in both directions.
- Focus moves to a sheet or dialog when it opens and back to its trigger when it
  closes. A route change focuses the page's heading.
- Text size in Appearance scales the root font size, and every layout holds at
  the largest step on a 390 px screen. A phone baseline checks it.

## 16. What ships when

| Phase | From this document |
|---|---|
| 3 | Sections 2 to 4; Chat in the Focus layout; Activity; Projects; Settings as a page; entry screens and PWA; the presence store, chip and mark; `AvatarSlot` as an empty component the Companion and Stage layouts can later fill; the performance budgets |
| 4 | Halt wired, banners, Security settings, tool card marks |
| 5 | Inbox, Memory |
| 11, 7, 6 | Approval cards and the needs-you tray, Plugins; Schedules; Skills |
| Not planned | Section 12 (avatar packs, the Companion layout, cues) and section 13 (read aloud, dictation, Live). Suggested order: avatar with text cues, read aloud, dictation, Live |

What the backend must add for the unplanned sections, each going through
`docs/api/` when it is built:

- Avatar packs: install, list and delete; an authenticated static path for pack
  files and Core; texture downscaling at install.
- Cues: the cue parser on the reply path, and a `message.cue` event carrying
  `{name, class, offset}` for the message it belongs to.
- Speech: the `/api/voice` socket, the speech lane and segmenter, TTS and STT
  adapters, turn detection, and the `voice` channel on entries.
- Settings keys for everything in "Avatar and voice".

## 17. Open questions

- **Cubism licensing.** Whether a self-hosted assistant others can install counts
  as an "Expandable Application" under Live2D's terms decides whether Eika can
  document avatar support publicly or keeps it a private build. Ask Live2D before
  any release that mentions it.
- **Mic permission in an installed iOS web app.** Older reports say the grant is
  forgotten between launches. Test on a real phone before designing Live's entry
  around a one-time sheet.
- **Echo cancellation on phone speakers** with page-played audio. Measure on the
  reference phone; the push-to-talk fallback is the answer if it is poor.
- **Worker rendering.** Decide from a profile of the avatar and a streaming reply
  on the reference phone, not in advance.
- **A second persona.** `PLAN.md` leaves more than one home session open. The
  rail's mark and the presence chip are where a switcher would go; nothing is
  reserved for it.
