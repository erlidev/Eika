# Eika

Eika is a Docker-native agentic coding harness. Agents work inside sandbox
containers, never on the host: every file read, file write, and command goes
through a workspace container. Around that it adds session trees, parallel
subagents, built-in web search, and a web UI.

The backend is Go, the frontend is React and TypeScript, and the whole stack
runs from one Docker Compose file.

## Quick start

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
envelope, health endpoints, frontend shell, and the compose stack. The agent
loop, sandboxes, persistence, and the UI proper land in later phases; see
`docs/PLAN.md`.
