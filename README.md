# Eika

Eika is a personal assistant that runs on hardware you own. It is being built
in phases (`docs/PLAN.md`), and this page describes what runs today, which is
the base the rest is built on.

Today you chat with a model you connect. A chat can be attached to the `home`
computer, a sandbox container with a disk that survives restarts, where the
model can run commands. The assistant itself does not exist yet: Eika has no
memory between chats, does nothing on its own, and has no layer that decides
what it may do. `docs/design/README.md` describes where it is going.

The backend is Go, the frontend is React and TypeScript, and the whole stack
runs from one Docker Compose file.

## What it does today

- **Chats** with any model behind an OpenAI-compatible API: hosted ones such as
  OpenAI, Anthropic, Gemini, OpenRouter, DeepSeek, Groq, and Mistral, or a
  local server such as llama.cpp, vLLM, Ollama, and LM Studio. Replies stream,
  with reasoning and measured context use; you can attach images, rewind, and
  inspect the exact request a model was sent. Long chats are compacted.
- **The home computer**, made when the harness starts. It is a container
  with a volume at `/home/eika`, CPU, memory, and process limits, and network
  access you can restrict to an allowlist. A chat started on it gets `bash`,
  and every command runs in that container, never on your host.
- **Web search and page reading** through a bundled SearXNG, with optional
  search providers.
- **MCP servers**, remote or run inside the home computer, whose tools a chat
  can use.
- **Profiles**, which save the model, prompt, tools, and sampling for a chat.

A chat that is not attached to a computer has no file or command tools.

## Quick start

With Docker and its Compose plugin installed, from a clone of this
repository:

```sh
docker compose up -d
```

The first start builds the harness and the default sandbox image, which takes
a few minutes. Then open http://localhost:8080. A guided setup walks you
through the rest:

1. Choose the password this harness signs in with.
2. Connect a model provider. Add as many as you like.
3. Pick the models chats may use. Eika lists what the endpoint serves and
   suggests each model's limits; a test button sends one short request.
4. Check that Eika can start sandbox containers.

Nothing needs an environment file. Everything the setup asks, and the sandbox
image besides, is in Settings afterwards. API keys are encrypted before they
are stored.

`make local` runs the same `docker compose up`, `make local-logs` follows the
logs, and `make local-down` stops the stack. Docker volumes keep the database
and the key that encrypts stored credentials. The stack listens
on 127.0.0.1 only.

### Backup

```sh
make backup
```

writes one backup set to `backups/eika-backup-<UTC time>/` (override the place
with `EIKA_BACKUP_DIR`). The directory is mode 0700 and each file 0600. A set
holds exactly:

- `postgres.dump`: the database (`pg_dump -Fc`), sealed credentials included.
- `memory.tar`: the whole memory repository, git history included, read from
  the memory computer's volume `eika-cx-<id>`, where `<id>` is the id this
  install recorded in its database (`core.memory_computer`). `EIKA_MEMORY_VOLUME` names another volume.
- `skills.tar`: the installed skills volume, `eika-cx-<id>`, named by
  `core.skills_computer`. `EIKA_SKILLS_VOLUME` selects another volume for backup.
- `secret.key`: the key that seals stored credentials. Without it the dump's
  sealed settings cannot be read, so the set is refused when it is missing.
- `manifest.json`: format, UTC time, Eika commit (and whether the tree was
  modified), harness image id, the last migration applied, and the SHA-256 and
  size of each file.

Not included: the files of other computers (their volumes), the sandbox images,
the guard and search services, `.env`. Back those up separately if you need them.

Eika is stopped while the set is taken, so the database, the memory repository
the skills volume and the key match each other, and started again afterwards, also when the backup
fails. Expect it to be unavailable for the length of the dump and copy, normally
seconds. The postgres service must be running. Nothing secret is printed; the
set itself is secret, so keep it somewhere only you can read. A set is built in a
hidden `.eika-backup-*.partial` directory and renamed only when complete; a
failed run leaves none. `make restore` reads this format.

### Restore

```sh
docker compose up -d postgres      # a new stack needs only its database running
make restore BACKUP=backups/eika-backup-<UTC time>
```

Restore only into a stopped, empty target: a new stack, or one whose data you
removed (`docker compose down -v` destroys it). Before it writes anything,
`make restore` checks the whole set: `manifest.json`, `postgres.dump`,
`memory.tar`, `skills.tar` and `secret.key` are all there (format 1 sets do not
contain `skills.tar`), every size and SHA-256 matches the
manifest, the dump and the archive can be read, and the set's migration is not
newer than this build (upgrade Eika first if it is). It then refuses, writing
nothing, when Eika is running, the database holds any table, `secret.key`
already exists in the data volume, or the memory volume is not empty. Then it
restores the database, the key (unchanged, mode 0600, owned by the harness
user) and the memory repository with its git history into the volume
`eika-cx-<id>`, named from the `core.memory_computer` id in the restored
database, and starts the stack. Core's startup reconcile verifies the memory
index against the restored files and rebuilds what is missing; look for the
`memory reconciled` line in `docker compose logs eika`, which the command
prints. Skills are restored to their recorded volume and Core reconciles them at startup.
If that volume belongs to another stack, restore selects a new ID, as for memory.
Sign in with the backed-up password; sealed credentials open because
the key came back with them.

**Restoring beside the source.** The memory volume `eika-cx-<id>` is named per
Docker host, not per stack. When the set's volume exists and is not empty, it
belongs to another stack on this host (typically the backed-up stack, still
running). It is never written: the repository goes into a volume named from a
fresh id, and `core.memory_computer` in the restored database is changed to
that id before Core starts. So you can back up a running stack and restore it
into a separate empty compose project (`COMPOSE="docker compose -p other"`) on
the same host, with the source intact. Other computers' files and the home
computer's volume are not in a set; Eika makes the home container again.

`FORCE=1 make restore BACKUP=...` is **destructive**: it drops the target
database and replaces it, overwrites the key file and empties the target's own memory
and skills volumes (the ones its database records, never another stack's) first. Use it only when the target holds nothing you need. Eika must
still be stopped. If a restore fails part-way the target is partly written;
fix the cause and rerun with `FORCE=1`. Nothing secret is printed.

`make restore-test` runs the whole cycle on throwaway compose projects of its
own (never your stack, never `make smoke`'s): it populates a source stack
(sign-in password, a sealed provider key, sessions with entries, memories
active, pending and archived), backs it up, checks that incomplete, damaged and
too-new sets are rejected and that a running or filled target is refused, restores
into a new stack beside it and checks the sessions, memories, sealed
key, key mode and git history, then repeats with `FORCE=1` and checks the
source, which kept running throughout, is unchanged. It needs about
3 GB of free memory and takes a few minutes; both stacks are removed afterwards,
and the images `eika:restoretest` and `eika-sandbox:restoretest` stay as a
build cache.

### Access from a phone

Install Tailscale on the host and phone, and connect both to the same tailnet.
Enable HTTPS for the tailnet, then run this on the host:

```sh
tailscale serve --bg http://127.0.0.1:8080
```

Open the HTTPS address that Tailscale gives you on the phone. You can install
Eika from the browser's app or home screen menu. Tailscale Serve keeps access
within the tailnet without opening a public port. The app shell can load offline,
but chats and other data need a connection to Eika.

You can also use a reverse proxy. Set `public_url` to the HTTPS address for
OAuth metadata. If the frontend uses a different origin from the server, add
its host to `allowed_origins` in the harness configuration.

To change the published port or set a fixed API token for scripts, copy
`.env.example` to `.env` and edit it.

The harness mounts the Docker socket to start sandboxes, which gives it
root-equivalent access to the host. Run it only on a machine where that is
acceptable.

### An existing database must be reset

The schema was rewritten for the assistant, and `0001_baseline.sql` was
edited in place while that happened, so no database made by an earlier
version can be migrated and the harness does not try. Delete the stack's
volumes and start again:

```sh
docker compose down -v
```

This removes the database and the key that encrypts stored credentials, so
you set Eika up again. Computers from earlier versions (containers labelled
`eika.workspace`) are not adopted; remove them and their volumes yourself.
Later schema changes are forward migrations.

## Development

```sh
make check   # fmt, vet, lint, Go and web tests, then the visual suite
make dev     # harness on :8080 and Vite on :5173 against compose services
make smoke   # the whole stack in compose, from setup to a sandboxed run
make live    # the live tests against a real model (evals/README.md)
```

`make check` runs the Docker-dependent Go tests when a daemon is reachable
and skips them otherwise. `make dev` starts postgres and SearXNG on loopback
(`compose.dev.yaml`) and runs the harness on the host with its state in
`.dev/`. `make smoke` runs beside a `make local` stack, on port 18080.
`make live` needs a model endpoint in `~/.config/eika/live.env` and is never
part of `make check`.

## Documentation

- `AGENTS.md` — start here before changing the codebase.
- `docs/ARCHITECTURE.md` — how the pieces fit together.
- `docs/DECISIONS.md` — why they are built that way, and what is deferred.
- `docs/EXTENDING.md` — adding a tool, provider, search source, or UI page.
- `docs/STYLE_GUIDE.md` — coding and documentation rules.
- `docs/api/` — HTTP, WebSocket, and sandbox daemon wire contract.
- `docs/PLAN.md`, `docs/DEFERRED.md` and `docs/design/` — where Eika is going, and the phases that
  get it there.
