# Eika

Eika is a Docker-native agentic coding harness. Agents work inside sandbox
containers, never on the host: every file read, file write, and command goes
through a workspace container, with CPU, memory, and network limits you set.
Around that it adds session trees, parallel subagents, chats, web search, MCP
servers, agent profiles, and a web UI with a terminal, editor, and diff.

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
make check   # fmt, vet, lint, Go and web tests, then the visual suite
make dev     # harness on :8080 and Vite on :5173 against compose services
make smoke   # the whole stack in compose, from setup to a sandboxed run
```

`make check` runs the Docker-dependent Go tests when a daemon is reachable
and skips them otherwise. `make dev` starts postgres and SearXNG on loopback
(`compose.dev.yaml`) and runs the harness on the host with its state in
`.dev/`. `make smoke` runs beside a `make local` stack, on port 18080.

## Documentation

- `AGENTS.md` — start here before changing the codebase.
- `docs/ARCHITECTURE.md` — how the pieces fit together.
- `docs/DECISIONS.md` — why they are built that way, and what is deferred.
- `docs/EXTENDING.md` — adding a tool, provider, search source, or UI panel.
- `docs/STYLE_GUIDE.md` — coding and documentation rules.
- `docs/api/` — HTTP, WebSocket, and sandbox daemon wire contract.
