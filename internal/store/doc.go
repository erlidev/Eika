// Package store is Eika's PostgreSQL access layer.
//
// Open connects a pool, verifies it, and applies the embedded migrations in
// internal/store/migrations. Everything else is hand-written SQL in one file
// per entity: projects, workspaces, sessions, session entries, runs,
// subagents, settings, providers, models, and sign-in. Credentials arrive and
// leave sealed; the store never sees one in the clear. Queries return the
// domain structs defined here;
// nothing in this package knows about HTTP, the agent loop, or Docker.
//
// Operations that touch more than one row run in a transaction, which is why
// appending a session entry and moving the session head is one call
// (AppendEntry). A missing row is store.ErrNotFound; a unique violation is
// store.ErrConflict. The session tree model on top of this package is
// internal/session.
package store
