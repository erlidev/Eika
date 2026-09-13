// Package session is Eika's session tree: the entries one conversation is
// made of, the head a run continues from, and the branches and forks that
// grow out of it.
//
// Tree wraps internal/store and adds the domain on top of the rows: appending
// an entry at the head, moving the head to any ancestor to branch in place,
// forking a session at an entry into a session of its own, and rendering a
// compact Outline for the user interface. Path returns the entries from the
// root to the head, which is what becomes the provider conversation.
//
// Store implements agent.Store against the database: it records every message
// a run produces as one entry, and Load rebuilds an agent.Session from a
// session's path so a run can resume where it stopped. An assistant entry
// also records the workspace HEAD commit the response was produced at, which
// is what forking with a workspace clones at.
package session
