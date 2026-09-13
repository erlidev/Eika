// Package server exposes the Eika harness over HTTP.
//
// It owns the whole HTTP surface: the JSON API for projects, workspaces,
// sessions, runs, questions, and settings; the WebSocket event stream; bearer
// token authentication; the git hub mounted at its own prefix; and, when
// configured, the built frontend. Nothing else in internal/ imports server.
//
// Run is the composition of the process: it opens the store, builds the hub,
// the workspace host, the model set, and the tool registry, and serves. New
// takes those pieces as Deps instead, which is how the tests run the API
// against fakes. Handlers live one file per resource and are registered in
// routes.go; docs/api/http.md documents every route.
//
// A run of the agent loop is owned by the run manager in runs.go: one
// goroutine per run, at most one run per session, events emitted into the
// event.Bus that the event stream fans out. A run outlives the request that
// started it and ends when it finishes, when it is aborted, or when the
// harness shuts down.
package server
