// Package secret seals the credentials the user enters in the web UI, model
// API keys and git remote passwords, before they are written to the database.
//
// A Box holds one AES-256-GCM key. Load reads that key from a file the harness
// keeps in its own data volume, creating it on first start, so a copy of the
// database alone reveals no credential. The package depends only on the
// standard library.
//
// The entry points are Load, New, Box.Seal, and Box.Open.
package secret
