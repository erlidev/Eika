# Eika

Eika is a Docker-native agentic coding harness. Agents work inside sandbox
containers, never on the host: every file read, file write, and command goes
through a workspace container. Around that it adds session trees, parallel
subagents, built-in web search, and a web UI.

The backend is Go, the frontend is React and TypeScript, and the whole stack
runs from one Docker Compose file.

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
2. Connect a model provider: OpenAI, Anthropic, Google Gemini, OpenRouter,
   DeepSeek, Groq, Mistral, a local Ollama or LM Studio, or any other
   OpenAI-compatible endpoint. Add as many as you like.
3. Pick the models agents may use. Eika lists what the endpoint serves and
   suggests each model's limits; a test button sends one short request.
4. Check that Eika can start sandbox containers.
5. Add a first project: a directory on this machine or a git remote.

Nothing needs an environment file. Everything the setup asks, and the sandbox
image and subagent limits besides, is in Settings afterwards. API keys and git
tokens are encrypted before they are stored.

`make local` runs the same `docker compose up`, `make local-logs` follows the
logs, and `make local-down` stops the stack. Docker volumes keep the database,
the git hub, and the key that encrypts stored credentials. The stack listens
on 127.0.0.1 only; put a reverse proxy in front of it for remote access.

To change the published port or set a fixed API token for scripts, copy
`.env.example` to `.env` and edit it.

### Upgrading from an environment-configured version

Earlier versions read models from `deploy/eika.yaml` and the auth token, API
keys, and git credentials from `deploy/.env`. After updating:

- The stack now reads `.env` from the repository root. If your database was
  created with a `POSTGRES_PASSWORD` of your own, copy that line from
  `deploy/.env` to `.env`: the volume keeps the password it was created with.
- Open the UI. The guided setup asks for a password and a provider; add the
  models your `eika.yaml` declared there.
- Enter the credentials of private remote projects again under each
  project's settings, the gear in the project tree.
- `make local` used to run as the Compose project `eika-local`. Its data stays
  in the `eika-local_*` volumes; `docker compose -p eika-local up -d` runs the
  stack on it.

## Development

```sh
make web-install   # once, installs frontend dependencies
make check         # fmt, vet, staticcheck, Go tests, eslint, tsc, vitest
make dev           # harness on :8080 and Vite on :5173 against compose services
```

`make check` needs no Docker and no network once `make web-install` has run.
`make dev` starts postgres and SearXNG in Docker, published on loopback by
`compose.dev.yaml`, and runs the harness on the host with its state in
`.dev/`, so it needs Docker and a free port 5432.

## Documentation

- `AGENTS.md` — start here before changing the codebase.
- `docs/PLAN.md` — scope, decisions, phase status.
- `docs/ARCHITECTURE.md` — how the pieces fit together.
- `docs/EXTENDING.md` — adding a tool, provider, search source, or UI panel.
- `docs/STYLE_GUIDE.md` — coding and documentation rules.
- `docs/api/` — HTTP and WebSocket wire contract.

## Status

Phases 0 to 6 are complete: foundations, the agent core, sandboxes and
workspaces, persistence and session trees, the API and event stream, the web
UI core, and subagents. Configuration lives in the web UI with a guided
setup. Search, the terminal and editor, and hardening are next; see
`docs/PLAN.md`.
