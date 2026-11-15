package session

import (
	"context"
	"slices"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// A run continues from an entry by sending the path that ends at it to the
// model, so not every entry is a place a run can continue from. A model that
// asked for three tool calls needs all three answered before it is asked
// anything else: a path that stops at the assistant entry, or in the middle
// of the results, is a conversation every endpoint rejects.
//
// An entry is resumable when it is not a preamble and its path leaves no
// tool call unanswered. A preamble cannot be a branch point because a new
// snapshot must take effect together with its compaction.

// pendingCalls returns the tool calls still unanswered after e, given the
// ones its parent left unanswered. The result is a new slice, so an entry's
// pending set is never the same backing array as its parent's.
func pendingCalls(parent []string, e store.Entry) []string {
	m, ok, err := Message(e)
	if err != nil || !ok {
		// A system note or an event is not part of the conversation, so it
		// answers nothing and asks nothing.
		return parent
	}
	if len(m.ToolCalls) > 0 {
		next := make([]string, 0, len(parent)+len(m.ToolCalls))
		next = append(next, parent...)
		for _, c := range m.ToolCalls {
			next = append(next, c.ID)
		}
		return next
	}
	if m.Role != provider.RoleTool {
		return parent
	}
	next := slices.Clone(parent)
	if i := slices.Index(next, m.ToolCallID); i >= 0 {
		return slices.Delete(next, i, i+1)
	}
	return next
}

// PathResumable reports whether a run can continue from the end of a path.
// It is the rule itself, on entries already in hand: the outline applies it
// to every branch of a tree, and Tree.Resumable to the one path an entry
// sits on.
func PathResumable(path []store.Entry) bool {
	var pending []string
	for _, e := range path {
		pending = pendingCalls(pending, e)
	}
	return len(pending) == 0 && (len(path) == 0 || path[len(path)-1].Kind != store.KindPreamble)
}

// resumableSet reports for every entry whether a run can continue from it.
// The entries must be in write order, which is what store.Entries returns:
// an entry's sequence number is always higher than its parent's, so one pass
// sees every parent before its children.
func resumableSet(entries []store.Entry) map[string]bool {
	pending := make(map[string][]string, len(entries))
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		left := pendingCalls(pending[e.ParentID], e)
		pending[e.ID] = left
		out[e.ID] = len(left) == 0 && e.Kind != store.KindPreamble
	}
	return out
}

// Resumable reports whether a run can continue from one of a session's
// entries: moving the head there leaves a conversation the model can be asked
// to continue. An entry that is not the session's is
// store.ErrNotFound.
func (t *Tree) Resumable(ctx context.Context, sessionID, entryID string) (bool, error) {
	path, err := t.store.EntryPath(ctx, sessionID, entryID)
	if err != nil {
		return false, err
	}
	return PathResumable(path), nil
}
