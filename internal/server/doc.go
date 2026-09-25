// Package server exposes the Eika harness over HTTP.
//
// It owns the whole HTTP surface: the JSON API for projects, workspaces,
// sessions, runs, questions, providers, models, profiles, a session's
// configuration and the context of its model requests, MCP servers, and
// settings;
// password sign-in and bearer token authentication; the WebSocket event
// stream; a workspace's files, commits, pushes, and the terminal socket it
// relays to the sandbox; the git hub mounted at its own prefix; and, when
// configured, the built frontend. Nothing else in internal/ imports server.
//
// Providers, models, and credentials are rows the UI edits. The server seals
// credentials with internal/secret and builds a provider client from a row
// for one run, probe, or test at a time.
//
// Run is the composition of the process: it opens the store, loads the
// sealing key, builds the hub, the workspace host, the provider registry, the
// tool registry, and the MCP pool (mcp.go, which also backs the pool with
// sealed rows and starts stdio servers through the host), and serves. New
// takes those pieces as Deps instead, which is how the tests run the API
// against fakes. Handlers live one file per resource and are registered in
// routes.go; docs/api/http.md documents every route.
//
// A run of the agent loop is owned by the run manager in runs.go: one
// goroutine per run, at most one run per session, events emitted into the
// event.Bus that the event stream fans out. A run outlives the request that
// started it and ends when it finishes, when it is aborted, or when the
// harness shuts down.
//
// What a run sends is resolved in configuration.go, the one place the rule
// lives: the model a run request names, the session's overrides, its
// profile, the model row, and the defaults, with the layer each value came
// from. A tool choice is resolved against the registry and the MCP pool by
// toolChosen in tools.go. The run manager, the context preview, and the
// profile and session configuration routes (profiles.go) all use it, and
// every model call a run makes is recorded in model_requests (requests.go).
package server
