# Scheduler

The scheduler makes Eika work while nobody is talking to it. It is native to Core,
not a plugin, because it decides when the GPU is spent. That is the one resource
a local assistant is short of. Cloud agents throttle background work to protect
plan limits; Duet checks connected accounts "every few hours". Eika throttles to
protect the user's own interactive latency and power bill.

It borrows durable jobs in isolated sessions, a recursion guard, and jobs the user can
see rather than inferred commitments (OpenClaw removed those in 2026) from OpenClaw
and Hermes. Triggers, nudges, pre-checks and active hours are deferred
(`DEFERRED.md`).

## 1. Concepts

| Concept | What it is |
|---|---|
| Routine | A named, user-visible job: when, what, as which agent, in which project, how to report |
| Run | One execution of a routine, recorded with outcome, tokens and duration |
| Wakeup | An agent's own request to resume its session later with a note (`wake_me`) |

## 2. Routines

`core.routines`:

| Column | Meaning |
|---|---|
| `name`, `description`, `enabled` | Identity. Agent-created routines start disabled until approved (section 5) |
| `schedule` | One of `at` (one shot, RFC 3339) or `cron` (5-field), with `timezone`. The `routine_create` tool turns "every weekday at 8" into one of these, and it is shown back before saving |
| `agent`, `project` | Who runs it |
| `prompt` | The instruction for each run |
| `max_runtime` | How long it may run, 24 h by default |
| `missed` | Run at most once on return, within a grace window of 6 h |
| `created_by`, `approved_grant` | `user` or an agent, and the Sentry grant (scope `routine`) its runs act under |

Each run is a new `routine` session, and its result reaches the user in the home
session with a push.

One routine ships disabled: **Morning brief**, on weekdays at 07:30, agent `eika`.

## 3. Wakeups

`wake_me {at | in, note}` is a tool for any agent with a session that continues
(home, subagent). At the time given, the note is appended to that session as a
user-side message: `<context>wakeup: <note>, scheduled <when></context>`. The
agent then runs at `background` priority. The session's prefix is untouched, so
the wakeup costs only its own tokens plus whatever the cache lost meanwhile.

A subagent waiting for something outside ("check back on the build in an hour")
uses a wakeup instead of holding a run open; the wakeup resumes that subagent, not
the home session. Wakeups are listed in Activity, and the
user can cancel them. An agent may hold at most `wakeups_max` (default 20)
pending wakeups.

## 4. Engine

- `internal/schedule`: one goroutine owned by `Server.Run` ticks every 15 s. It
  selects due routines and wakeups with `FOR UPDATE SKIP LOCKED`, marks them
  claimed, and computes the next time.
- A claimed job becomes a run (`core.routine_runs`) and a session run through the
  run manager at `background` priority.
- **No overlap.** A routine whose previous run is still going skips with a
  recorded reason. One run, one row.
- **Restarts.** A run that was `running` is recorded as interrupted (`runtime.md` 5).
  Jobs that came due while Core was down follow `missed`.

## 5. Safety

- **Creating a routine is a `schedule` action.** Sentry asks the user. The
  approval card shows when it runs, what it does, as which agent, and the grant
  it would run under. Enabling it creates that grant with scope `routine`.
- **Recursion guard.** Sessions of kind `routine` cannot create routines or
  wakeups beyond their own session. The tools return an error saying so.
- **Prompt scan at creation.** A routine's prompt goes through the deterministic
  scans, since an injected routine is the classic persistence attack. The approval
  card shows the prompt in full.

## 6. Tools and UI

- Tools: `routine_create`, `routine_list`, `routine_update` (an agent can change
  what it created; enabling still needs approval), `routine_delete`, and
  `wake_me`.
- UI: the Schedules page lists routines (next run, last outcome),
  run history with links to each run's session, and wakeups. An edit dialog shows the parsed schedule in words and
  the next five times.
