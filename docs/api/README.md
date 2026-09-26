# API reference

The wire contract between the harness and its clients. Go types live in the
package that owns them, TypeScript mirrors in `web/src/api/`; change both and
these files in the same commit.

- `http.md` — every HTTP route, authentication, and the error body.
- `events.md` — the event stream: transport, subscription and replay,
  topics, the envelope, and every event type.
- `eikad.md` — the sandbox daemon, whose only client is the harness.
- `contract.json` — the exact shape of every request, response, error, and
  event payload, generated from the Go wire types (`make contract`). Handler
  tests check responses against it; the frontend's mock harness checks
  requests, responses, and events. The Markdown files say what fields mean;
  this file says which fields exist.

Everything under `/api` requires `Authorization: Bearer <token>` (a sign-in
session or the deployment's API token) except `GET /api/healthz`, the three
routes that hand out a session, and the two WebSockets' `?token=` form. The
git hub at `/git/<project>.git` speaks Smart HTTP with per-workspace basic
auth.
