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
on 127.0.0.1 only; put a reverse proxy in front of it for remote access.

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
