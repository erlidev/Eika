package session

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
)

// previewLimit is how many runes of an entry's content a Node carries. It is
// enough for a tree row in the user interface and small enough that an
// outline of a long session stays one response.
const previewLimit = 80

// Node is one entry as the session tree in the user interface shows it: the
// shape of the tree plus enough content to recognise the entry, without its
// payload.
type Node struct {
	ID       string          `json:"id"`
	ParentID string          `json:"parent_id,omitempty"`
	Kind     store.EntryKind `json:"kind"`
	// Preview is the start of the entry's content on one line.
	Preview string `json:"preview"`
	// Commit is the workspace HEAD commit the entry was produced at, empty
	// when none was recorded. An entry with a commit can be forked with a
	// workspace.
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Outline returns every entry of a session as a Node, in write order, so that
// the caller can draw the whole tree. The session's head, which decides which
// branch is current, comes from Head.
func (t *Tree) Outline(ctx context.Context, sessionID string) ([]Node, error) {
	entries, err := t.store.Entries(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(entries))
	for _, e := range entries {
		nodes = append(nodes, Node{
			ID:        e.ID,
			ParentID:  e.ParentID,
			Kind:      e.Kind,
			Preview:   preview(e),
			Commit:    e.Commit,
			CreatedAt: e.CreatedAt,
		})
	}
	return nodes, nil
}

// preview renders the start of an entry's content on one line. An assistant
// turn that only asked for tools is described by the tools it asked for.
func preview(e store.Entry) string {
	if m, ok, err := Message(e); err == nil && ok {
		if text := strings.TrimSpace(m.Content); text != "" {
			return shorten(text)
		}
		if len(m.ToolCalls) > 0 {
			names := make([]string, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				names = append(names, c.Name)
			}
			return shorten("calls " + strings.Join(names, ", "))
		}
		return ""
	}
	var note struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(e.Payload, &note); err == nil && note.Text != "" {
		return shorten(note.Text)
	}
	return shorten(string(e.Payload))
}

// shorten reduces text to its first line, bounded by previewLimit runes.
func shorten(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i]) + " ..."
	}
	runes := []rune(text)
	if len(runes) > previewLimit {
		return strings.TrimSpace(string(runes[:previewLimit])) + " ..."
	}
	return text
}
