// Package server exposes the Eika harness over HTTP.
//
// It owns routing, the shared HTTP listener lifecycle, and, when configured,
// serving the built frontend. Handlers live in this package and are registered
// in routes.go; nothing else in internal/ imports server.
//
// As of phase 0 the only routes are the health checks (/healthz and
// /api/healthz). The API, the WebSocket event stream, and bearer token auth
// arrive in phase 4.
package server
