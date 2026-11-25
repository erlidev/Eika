// Package session is Eika's session tree: the entries one conversation is
// made of, the head a run continues from, and the branches that grow out of it.
//
// Tree wraps internal/store and adds the domain on top of the rows: appending
// an entry at the head, moving the head to any ancestor to branch in place, and
// rendering a compact Outline for the user interface. Path returns the entries
// from the root to the head, which is what becomes the provider conversation.
//
// Not every entry is a place a branch can start. PathResumable is the rule:
// a run can continue only when the entry is not a preamble and its path leaves
// no tool call unanswered. A replacement preamble takes effect with its
// compaction, and a model needs every tool call answered. Outline reports it per
// node and Tree.Resumable for one entry, and the server checks it before
// moving a head.
//
// Store implements agent.Store against the database: it records every message
// a run produces as one entry, and Load rebuilds an agent.Session from a
// session's path so a run can resume where it stopped.
//
// A compaction is an entry too. Messages folds a path into the conversation
// it stands for, where each compaction entry replaces what came before it
// with its summary and the messages it kept; the entries it covers stay, so
// a branch from before it has the whole conversation.
package session
