// Package event defines the envelope and type names for everything Eika
// streams to a client.
//
// The agent loop, workspace lifecycle, and subagent machinery all emit the
// same Event value: a type name, a topic to route it to, a UTC timestamp, and
// an opaque JSON payload owned by the emitting package. Keeping the envelope
// here lets the server fan events out and the frontend decode them without
// either side importing the packages that produce them.
//
// The wire contract is documented in docs/api/ and mirrored in
// web/src/api/. Adding an event type means adding a constant here, a payload
// struct in the owning package, and the matching documentation and TypeScript
// type in the same change.
//
// The package depends only on the standard library.
package event
