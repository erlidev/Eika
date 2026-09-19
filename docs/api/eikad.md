# eikad API

The sandbox daemon's HTTP API. It runs inside every workspace container on
port 7000 and is the only way the harness reaches a workspace. The Go wire
types live in `internal/eikad/api.go`; the client is
`internal/executor/sandbox`.

Every route except `GET /healthz` requires `Authorization: Bearer <token>`,
where the token is the `EIKAD_TOKEN` the harness generated for that workspace.
A missing or wrong token is `401`.

Every `path` is relative to the workspace root (`/workspace` by default). An
absolute path inside the root is accepted; a path that leaves the root, by
`..` or through a symlink, is `403`. A missing path is `404`.

Errors are `{"error": "<message>"}` with a 4xx or 5xx status.

## GET /healthz

The only unauthenticated route. Returns `{"status": "ok"}`. It is the
container health check.

## POST /exec

Runs a command and streams its output.

Request:

| Field | Type | Meaning |
|---|---|---|
| `command` | string | Program to run, or the script when `shell` is set. Required. |
| `args` | string[] | Arguments. Ignored when `shell` is set. |
| `shell` | bool | Run `command` through `sh -c`. |
| `dir` | string | Working directory, relative to the root. Empty means the root. |
| `env` | string[] | Additional `KEY=VALUE` entries. |
| `timeout_ms` | number | Kill the command after this many milliseconds. Zero means no limit. |
| `stdin` | base64 string | Fed to the command. Sent up front; `/pty` is the interactive route. |

Response: `200` with `Content-Type: application/x-ndjson`, one JSON object per
line, flushed as the command produces output.

| Field | Type | Meaning |
|---|---|---|
| `stream` | string | `stdout` or `stderr` on an output frame. |
| `data` | base64 string | The chunk of output. |
| `exit_code` | number | Present on the final frame of a command that ran. |
| `timed_out` | bool | The command was killed by its timeout. |
| `truncated` | bool | The command produced more than 8 MiB of output; the rest was dropped. |
| `error` | string | The command could not be started or waited for. No more frames follow. |

The command runs as the daemon's user. `EIKAD_TOKEN` is removed from its
environment, so a command inside the sandbox cannot read the daemon's token.

A command leads its own process group, and a timeout kills the whole group, so
a backgrounded grandchild cannot outlive the run or hold the output open.
Output is capped at 8 MiB per run; past that the daemon stops forwarding and
sets `truncated` on the final frame. The sandbox client turns that into an
`[eika: output truncated]` line on the command's stderr.

## GET /files?path=&max_bytes=

Returns the file's bytes as `application/octet-stream`, truncated at
`max_bytes` or at the daemon's own limit (16 MiB), whichever is smaller.
Reading a directory is `400`.

## PUT /files?path=

Writes the request body to the file, creating parent directories. Returns the
resulting file as a file object. Bodies over 64 MiB are `413`.

## GET /stat?path=

Returns one file object.

## GET /list?path=

Returns `{"entries": [<file object>, ...]}` for the direct children of a
directory.

### File object

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Base name. |
| `path` | string | Path relative to the workspace root. |
| `size` | number | Size in bytes. |
| `mode` | number | Go file mode bits. |
| `mod_time` | RFC 3339 timestamp, UTC | Modification time. |
| `is_dir` | bool | Whether it is a directory. |

## GET /pty?shell=&dir=&rows=&cols=

Upgrades to a WebSocket carrying an interactive shell. Messages are JSON text
frames in both directions.

| Field | Type | Meaning |
|---|---|---|
| `type` | string | `input`, `output`, `resize`, or `exit`. |
| `data` | base64 string | Terminal input (`input`) or output (`output`). |
| `rows`, `cols` | number | New window size on `resize`. |
| `exit_code` | number | The shell's exit status on `exit`. |

The client sends `input` and `resize`; the daemon sends `output` and, once the
shell exits, a final `exit` before closing.

## GET /watch?path=

Upgrades to a WebSocket that reports changes under `path`. The daemon polls
once a second and compares modification times and sizes, so a change is
reported within about a second of happening.

| Field | Type | Meaning |
|---|---|---|
| `path` | string | Changed file, relative to the workspace root. |
| `kind` | string | `created`, `modified`, or `deleted`. |
| `is_dir` | bool | Whether the changed file is a directory. |
| `time` | RFC 3339 timestamp, UTC | When the daemon observed the change. |

The first snapshot is taken before the connection is accepted, so no change
made after the client connects is missed. At most 100 000 files are watched.

## `eikad filter` (command)

Not a route: a one-shot mode of the same binary, run through `POST /exec` as
`/usr/local/bin/eikad filter`. It runs a web_fetch filter, JavaScript the
model wrote, inside the sandbox, and never listens or needs `EIKAD_TOKEN`.
It reads one JSON request from stdin and writes one JSON outcome to stdout;
it exits 1, with the reason on stderr, only when the request cannot be read.

Request:

| Field | Type | Meaning |
|---|---|---|
| `markdown` | string | The page, or the section of it the read selected. |
| `source` | string | The filter: an expression, or a function body that returns. |
| `timeout_ms` | number | Bound on the run. Default 2000. |
| `tokens` | number | Budget the answer is cut to. Default 10 000. |

The filter sees `text`, `lines`, `sections` (`{heading, level, text, index,
from, to}`), `grep(re, ctx?)`, and `code(lang?)`, read-only, and nothing else:
no network, no timers, no module loader.

Outcome:

| Field | Type | Meaning |
|---|---|---|
| `kind` | string | `ok`, `empty` (ran and selected nothing), or `error`. |
| `text` | string | The answer cut to the budget for `ok`; the message otherwise. An `empty` message is a map of the page's headings. |
| `footer` | string | For an `ok` answer that fit, its coordinate space: `[filtered: ~K of ~T tokens · … sections · N lines]`. |
| `truncated` | bool | The answer was cut to the budget. |
| `stats` | object | `sections`, `kept_sections`, `lines`, `total_tokens`, `kept_tokens`, `sandbox_ms`. |
