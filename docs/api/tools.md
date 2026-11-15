# File and delegation tools

## File tools

These built-in tools require a computer. Every file operation uses its
executor. They are available in the worker definition; runtime adoption of
agent definitions follows in P2-08. Current computer sessions can select them.
They have no new HTTP routes or events.

Arguments are JSON objects with exactly the required string fields below.
Unknown or duplicate fields, null, invalid UTF-8, unpaired Unicode surrogates
and NUL are errors. The encoded argument limit is 12,608,512 bytes. A path has
1 to 4096 UTF-8 bytes and no surrounding whitespace. Paths are relative to the
working directory, or absolute inside the computer root. The working directory
is a worker's project directory, else the computer root; `bash` starts there
too. Traversal or symlinks outside the
root are refused. Files must contain UTF-8 text without NUL for reads and edits.

| Tool | Required fields | Result |
|---|---|---|
| `file_read` | `path`: string | Exact text, including whitespace, from a regular file of at most 32 KiB. An empty file returns `(empty file)`. |
| `file_write` | `path`: string, `content`: string | Creates or replaces a file with at most 1 MiB of text. Empty content is valid. Creates parent directories. |
| `file_edit` | `path`: string, `old_text`: string, `new_text`: string | Replaces exactly one occurrence. Empty `old_text` is refused; empty `new_text` deletes the match. Original and result must each fit in 1 MiB. |

All sizes count UTF-8 bytes. An edit counts overlapping matches as ambiguous.
Write and edit success text reports the number of bytes written. Reads of
larger files return an error; use `bash` to select a range. Calls have a
two-minute timeout. No match, multiple matches, missing files and invalid
arguments return normal tool errors with `is_error: true`.

Writes use atomic replacement: the new contents are synced to disk before
they replace the file. Existing permissions are preserved, and a file without
the owner write bit is refused as a permission error. A failed write leaves
existing contents intact. These tools do not lock files against
concurrent shell commands or other writers. Coordinate changes to the same
file; an edit can overwrite a change made after its read.

## Delegation tools

Eika's `subagent_start`, `subagent_message`, `subagent_status` and
`subagent_cancel` need no computer and exist only in the `eika` definition.
The server runs them for the home session alone; any other caller gets an
error result, so a subagent cannot delegate. Arguments follow the rules above,
with these fields:

| Tool | Fields | Result |
|---|---|---|
| `subagent_start` | `agent`: `worker` or `researcher`, `name`, `brief`, optional `project` slug | Starts the subagent and returns at once. An existing name returns that subagent unchanged, with a note to use `subagent_message`. |
| `subagent_message` | `subagent`, `text`, optional `question_id` | Answers the question with this id, or resumes an idle, failed or cancelled subagent when no id is given. A running one does not take new work; the note says so. |
| `subagent_status` | optional `subagent` | Without a name, the board: every subagent with `report` as the first line of its last report. With one, also `brief` and the whole `report`. |
| `subagent_cancel` | `subagent` | Stops the current run and waits for it; the subagent stays resumable. |

A name has 1 to 64 lowercase letters, digits or single separating hyphens. A
brief or message is nonempty, without NUL, and at most 64 KiB. Results are
JSON objects with `name`, `agent`, `state`, and where set `project`,
`waiting_on`, `last_active_at` and `report`; start, message and cancel add a
`note` for the model. An unknown name or project is an error result.

### report

`report` is in the `worker` and `researcher` definitions only and needs no
computer. It ends a subagent's assignment with its findings:

| Field | Meaning |
|---|---|
| `summary` | Required, nonempty, at most 32 KiB, without NUL. The outcome in its first line, then detail and assumptions. |
| `outputs` | Optional list of at most 20 strings of at most 1 KiB: files, links, figures. |
| `follow_ups` | Optional list of the same bounds: what Eika should do next. |

The result is stored as the session's `result` (`summary`, then `Outputs:` and
`Follow-ups:` lists when not empty), replacing the last report, and
`subagent_status` shows it. A call when no assignment is running is an error
result. A run that ends without calling it is sent "Draft your report now with
the report tool." once; if it still has not reported, its final assistant
message becomes the report. Failed and cancelled runs are not asked.

At most `subagent_max_running` subagents run at once (a setting, default 4);
Starting or resuming an execution past the limit returns an error result.
A waiting question keeps its reservation, so an answer needs no new one. Aborting Eika's reply does not stop its subagents.

A worker's session uses the home computer. Its runs create the project
directory when it is missing, run commands there and resolve relative paths
there; a directory outside the computer root refuses the run. A researcher has
no computer. A subagent's run uses the run manager at `foreground` priority
when an interactive home turn starts or messages it, else `background`.
Aborting its run, `subagent_cancel` and shutdown record it `cancelled`.

## ask_user

Workers and researchers use `ask_user` to ask Eika when unclear intent would
change the result. Arguments are `question` (required, nonempty, at most 2048
UTF-8 bytes), optional `options` (at most 10 nonempty strings, each at most 128
bytes), and optional `allow_free_text` (boolean, default false). NUL, invalid
UTF-8, unpaired surrogates, duplicate or unknown fields, null values and trailing
JSON are refused. With no options, free
text is always allowed.

The child becomes `waiting`; `waiting_reason` holds the question id, question
and choices as a JSON array.
A home notice carries the question and starts a background home turn when
home is idle. Eika supplies the answer through `subagent_message`, or asks the
user in its reply and relays the answer. An answer must carry the question id
from the notice as `question_id`. A missing or stale id cannot answer a later
question; a stale id cannot start new work. The note says when no question is
waiting. An ended question notice is marked no longer pending before delivery.
A closed question accepts only an
exact option. An invalid answer leaves it waiting. An accepted answer becomes
the existing tool call's text result and clears the waiting reason. Cancel
ends the wait; a restart ends the run and repairs its unanswered call. No
question row or new HTTP route is added. Legacy profile sessions retain the
question broker and HTTP answer route.
