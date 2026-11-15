# File tools

These built-in tools require a computer. Every file operation uses its
executor. They are available in the worker definition; runtime adoption of
agent definitions follows in P2-08. Current computer sessions can select them.
They have no new HTTP routes or events.

Arguments are JSON objects with exactly the required string fields below.
Unknown or duplicate fields, null, invalid UTF-8, unpaired Unicode surrogates
and NUL are errors. The encoded argument limit is 12,608,512 bytes. A path has
1 to 4096 UTF-8 bytes and no surrounding whitespace. Paths are relative to the
computer root, or absolute inside that root. Traversal or symlinks outside the
root are refused. Files must contain UTF-8 text without NUL for reads and edits.

| Tool | Required fields | Result |
|---|---|---|
| `file_read` | `path`: string | Exact text, including whitespace, from a regular file of at most 32 KiB. |
| `file_write` | `path`: string, `content`: string | Creates or replaces a file with at most 1 MiB of text. Empty content is valid. Creates parent directories. |
| `file_edit` | `path`: string, `old_text`: string, `new_text`: string | Replaces exactly one occurrence. Empty `old_text` is refused; empty `new_text` deletes the match. Original and result must each fit in 1 MiB. |

All sizes count UTF-8 bytes. An edit counts overlapping matches as ambiguous.
Write and edit success text reports the number of bytes written. Reads of
larger files return an error; use `bash` to select a range. Calls have a
two-minute timeout. No match, multiple matches, missing files and invalid
arguments return normal tool errors with `is_error: true`.

Writes use atomic replacement and preserve existing permissions. A failed
write leaves existing contents intact. These tools do not lock files against
concurrent shell commands or other writers. Coordinate changes to the same
file; an edit can overwrite a change made after its read.
