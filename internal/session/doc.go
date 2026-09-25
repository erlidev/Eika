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
// Not every entry is a place a branch can start. PathResumable is the rule:
// a run can continue from an entry only when the path down to it leaves no
// tool call unanswered, because a model that asked for three calls needs all
// three answered before it is asked anything else. Outline reports it per
// node and Tree.Resumable for one entry, and the server checks it before
// moving a head or forking.
//
// Store implements agent.Store against the database: it records every message
// a run produces as one entry, and Load rebuilds an agent.Session from a
// session's path so a run can resume where it stopped. An assistant entry
// also records the workspace HEAD commit the response was produced at, which
// is what forking with a workspace clones at.
package session
