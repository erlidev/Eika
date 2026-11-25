// Package server exposes the Eika harness over HTTP.
//
// It owns the whole HTTP surface: the JSON API for the list of computers,
// sessions, runs, questions, providers, models, the context of model requests, MCP servers, and
// settings;
// password sign-in and bearer token authentication; the WebSocket event
// stream; and, when configured, the built frontend. Nothing else in internal/ imports server.
//
// Providers, models, and credentials are rows the UI edits. The server seals
// credentials with internal/secret and builds a provider client from a row
// for one run, probe, or test at a time.
//
// Run, in composition.go, is the composition of the process: it opens the
// store, loads the sealing key, builds the computer host, the
// provider registry, the tool registry, and the MCP pool (mcp_pool.go, which
// starts stdio servers through the host; mcp_store.go backs the pool with
// sealed rows), and serves. New takes those pieces as Deps instead, which is
// how the tests run the API against fakes. Handlers live one file per
// resource and are registered in routes.go; docs/api/http.md documents
// every route.
//
// A run of the agent loop is owned by the run manager in run_manager.go:
// one goroutine per run, at most one run per session, events emitted into
// the event.Bus that the event stream fans out. A run outlives the request
// that started it and ends when it finishes, when it is aborted, or when
// the harness shuts down.
//
// Run configuration comes from agent definitions and model classes in
// run_config.go. Each model call is recorded in model_calls (calls.go).
//
// The home computer's sandbox, its limits and egress, is checked in
// sandbox.go when the settings are saved and applied to its container by
// applyHomeSandbox in home.go; sandbox.go also answers the egress proxy's
// policy lookups (EgressPolicy); Run serves the proxy beside the API when the host
// can restrict egress.
package server
