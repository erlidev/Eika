// Package event defines the envelope and type names for everything Eika
// streams to a client.
//
// The agent loop, workspace lifecycle, and subagent machinery all emit the
// same Event value: a type name, a topic to route it to, a UTC timestamp, and
// a JSON payload whose struct lives in payload.go. Keeping the envelope and the
// payloads here lets the server fan events out and the frontend decode them
// without either side importing the packages that produce them.
//
// Emitter is how a producer hands events on; the server implements it in phase
// 4 and Discard drops them until then.
//
// The wire contract is documented in docs/api/events.md and mirrored in
// web/src/api/events.ts. Adding an event type means adding a constant here, a
// payload struct in payload.go, and the matching documentation and TypeScript
// type in the same change.
//
// The package depends only on the standard library.
package event
