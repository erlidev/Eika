# Scheduler

The scheduler makes Eika work while nobody is talking to it. It is native to Core,
not a plugin, because it decides when the GPU is spent. That is the one resource
a local assistant is short of. Cloud agents throttle background work to protect
plan limits; Duet checks connected accounts "every few hours". Eika throttles to
protect the user's own interactive latency and power bill.

It borrows these patterns from OpenClaw and Hermes:

- durable jobs in isolated sessions
- cache-friendly nudges in an existing session
- deterministic pre-checks before a model wakes
- a token for a silent result
- active hours and idle-only runs
- a recursion guard
- jobs the user can see rather than inferred commitments (OpenClaw removed those
  in 2026)

## 1. Concepts

| Concept | What it is |
|---|---|
| Routine | A named, user-visible job: when, what, as which agent, in which project, how to report |
| Run | One execution of a routine, recorded with outcome, tokens and duration |
| Wakeup | An agent's own request to resume its session later with a note (`wake_me`) |
| Trigger | An event source that starts a routine: a webhook, a plugin notification or resource change, a file change on a computer, or an interval watch |

## 2. Routines

`core.routines`:

| Column | Meaning |
|---|---|
| `name`, `description`, `enabled` | Identity. Agent-created routines start disabled until approved (section 5) |
| `schedule` | One of `at` (one shot, RFC 3339), `every` (duration), `cron` (5-field), with `timezone`. Natural-language input ("every weekday at 8") is parsed in the UI and by the `routine_create` tool into one of these, and shown back before saving |
| `trigger` | Instead of a schedule: `{kind: webhook}`, `{kind: plugin, plugin, notification or resource}`, `{kind: file, computer, path}` |
| `agent`, `project`, `class` | Who runs it. `class` overrides the agent's class (a cheap `fast` digest, for example) |
| `prompt` | The instruction for each run |
| `context` | `fresh` (a new `routine` session per run, the default), `continue` (one long `routine` session that keeps its own history, Hermes's `continuity`), or `nudge` (a user-side message into the home session, cache-friendly, only when the work needs the conversation) |
| `precheck` | Optional command on a computer, run before any model call. Exit code 0 with stdout different from the last run wakes the model, and stdout goes into the prompt. Anything else records "nothing new" and ends. This is Hermes's script-only mode |
| `report` | `always`, `when_noteworthy` (the run ends with `NO_REPLY` to stay silent) or `never` |
| `active_hours`, `idle_only`, `max_runtime`, `budget_class` | When it may run. Idle-only means no interactive lease in the last 10 minutes |
| `missed` | `run_once` (run at most once on return, within a grace window of 6 h by default) or `skip` |
| `created_by`, `approved_grant` | `user` or an agent, and the Sentry grant (scope `routine`) its runs act under |

Defaults that ship disabled:

- **Morning brief.** Weekdays at 07:30, agent `eika`, `fresh`, `report: always`.
- **Check-in.** Every 3 h inside active hours, agent `eika` with read-only plugins,
  `report: when_noteworthy`. It looks across connected plugins for things that
  need the user, the way dots' proactive research does. Its runs get only read
  effects: the grant it is created with allows reads and nothing else.
- **Dream.** Nightly (`memory.md` 7). This one ships enabled.

## 3. Wakeups

`wake_me {at | in, note}` is a tool for any agent with a session that continues
(home, direct, task). At the time given, the note is appended to that session as a
user-side message: `<context>wakeup: <note>, scheduled <when></context>`. The
agent then runs at `background` priority. The session's prefix is untouched, so
the wakeup costs only its own tokens plus whatever the cache lost meanwhile.

A task waiting for something outside ("check back on the build in an hour") uses
a wakeup instead of holding a run open. Wakeups are listed in Activity, and the
user can cancel them. An agent may hold at most `wakeups_max` (default 20)
pending wakeups.

## 4. Engine

- `internal/schedule`: one goroutine owned by `Server.Run` ticks every 15 s. It
  selects due routines and wakeups with `FOR UPDATE SKIP LOCKED`, marks them
  claimed, and computes the next time.
- A claimed job becomes a run (`core.routine_runs`) and, when its precheck
  passes, a session run through the run manager at `background` priority.
- **No overlap.** A routine whose previous run is still going skips with a
  recorded reason. One run, one row.
- **Restarts.** Runs that were `running` resume (`runtime.md` 5). Jobs that came
  due while Core was down follow `missed`.
- **Pre-flight.** Before claiming, the engine checks that the agent, project,
  plugins and model class still exist and are usable. A routine that fails is
  marked `blocked` with the reason, before any token is spent. Hermes calls this
  `blocked_config`.
- **Budget.** `background` runs count against the daily background budget
  (`runtime.md` 3). Over budget, runs defer to the next window and say so.
- **Quiet hours** delay `report` delivery, not the run. A result found at 3 am is
  delivered at the end of quiet hours unless it is urgent. A `decision` model, or
  `fast`, judges urgency with the question `needs_attention_now`.

## 5. Safety

- **Creating a routine is a `schedule` action.** Sentry asks the user. The
  approval card shows when it runs, what it does, as which agent, and the grant
  it would run under. Enabling it creates that grant with scope `routine`.
- **Recursion guard.** Sessions of kind `routine` cannot create routines or
  wakeups beyond their own session. The tools return an error saying so.
- **Prompt scan at creation.** A routine's prompt goes through Sentry ingest.
  An injected routine is the classic persistence attack, so a routine created
  from a tainted session is flagged in its approval card.
- Webhook triggers get a random path secret per routine
  (`/hooks/<routine id>/<secret>`). The payload is untrusted content and arrives
  wrapped.

## 6. Tools and UI

- Tools: `routine_create`, `routine_list`, `routine_update` (an agent can change
  what it created; enabling still needs approval), `routine_delete`, and
  `wake_me`.
- UI: the Schedules page lists routines (next run, last outcome, budget used),
  run history with links to each run's session, wakeups, triggers, the background
  budget and quiet hours. An edit dialog shows the parsed schedule in words and
  the next five times.
