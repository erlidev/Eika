# Eika

Eika is a Docker-native agentic coding harness. Agents work inside sandbox
containers, never on the host: every file read, file write, and command goes
through a workspace container. Around that it adds session trees, parallel
subagents, built-in web search, and a web UI.

The backend is Go, the frontend is React and TypeScript, and the whole stack
runs from one Docker Compose file.

## Quick start

For a local run that does not create an environment file or build output in
the repository, use:

```sh
OPENAI_API_KEY=your-key make local
```

Open http://localhost:8080 and use `dev-token` as the bearer token. Run
`make local-logs` to follow the logs and `make local-down` to stop the stack.
Docker volumes keep the database and hub data. The command does not write
generated files to the repository. Its containers and volumes use the
isolated `eika-local` Compose project name.

For a configured deployment, use:

```sh
cp deploy/.env.example deploy/.env   # then fill in EIKA_AUTH_TOKEN and SEARXNG_SECRET
make sandbox                         # build the default workspace image
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
```

The UI is on http://localhost:8080 and the health check is
http://localhost:8080/healthz.

## Development

```sh
cp deploy/.env.example deploy/.env   # make dev needs it: POSTGRES_PASSWORD, SEARXNG_SECRET
make web-install   # once, installs frontend dependencies
make check         # fmt, vet, staticcheck, Go tests, eslint, tsc, vitest
make dev           # harness on :8080 and Vite on :5173 against compose services
```

`make check` needs no Docker and no network once `make web-install` has run.
`make dev` starts the postgres and searxng containers, so it needs both.

## Documentation

- `AGENTS.md` — start here before changing the codebase.
- `docs/PLAN.md` — scope, decisions, phase status.
- `docs/ARCHITECTURE.md` — how the pieces fit together.
- `docs/EXTENDING.md` — adding a tool, provider, search source, or UI panel.
- `docs/STYLE_GUIDE.md` — coding and documentation rules.
- `docs/api/` — HTTP and WebSocket wire contract.

## Status

Phase 0 (foundations) is complete: module layout, configuration, event
envelope, health endpoints, frontend shell, and the compose stack. Phase 2
(sandboxes and workspaces) is complete: the `eikad` sandbox daemon, the
sandbox executor, workspace containers and volumes, and the git hub. The agent
loop, persistence, and the UI proper land in later phases; see `docs/PLAN.md`.
