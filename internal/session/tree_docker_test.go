//go:build docker

package session_test

import (
	"errors"
	"os"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m))
}

// newTree returns a tree and an empty session in a computer of its own.
func newTree(t *testing.T) (*session.Tree, store.Session) {
	t.Helper()
	st := storetest.Open(t)
	ctx := t.Context()
	cx, err := st.CreateComputer(ctx, store.Computer{Name: "fix-build", State: "running"})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "fix the build"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session.NewTree(st), sess
}

// A run continues the branch the head points at, with each compaction on it
// folded in, and appending after a moved head branches the tree in place.
func TestLoadFollowsTheHeadAndFoldsCompactions(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()
	st := session.NewStore(tree)
	appendText := func(m provider.Message) store.Entry {
		t.Helper()
		e, err := tree.AppendMessage(ctx, sess.ID, m)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		return e
	}
	contents := func() []string {
		t.Helper()
		loaded, err := st.Load(ctx, sess.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		var out []string
		for _, m := range loaded.Conversation.Messages() {
			out = append(out, m.Content)
		}
		return out
	}

	if _, err := tree.Head(ctx, sess.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Head of an empty session = %v, want ErrNotFound", err)
	}
	appendText(provider.UserMessage("one"))
	before := appendText(provider.AssistantMessage("two", nil))
	appendText(provider.UserMessage("three"))
	if err := st.AppendCompaction(ctx, sess.ID, "", agent.Compaction{Summary: "one and two", Kept: 1, TokensBefore: 1234}); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	appendText(provider.AssistantMessage("four", nil))
	summary := agent.SummaryMessage("one and two").Content
	if got := contents(); len(got) != 3 || got[0] != summary || got[1] != "three" || got[2] != "four" {
		t.Errorf("loaded conversation = %q, want the summary, three, and four", got)
	}

	// A branch from before the compaction has the whole conversation.
	if err := tree.SetHead(ctx, sess.ID, before.ID); err != nil {
		t.Fatalf("SetHead: %v", err)
	}
	branch := appendText(provider.UserMessage("three again"))
	if got := contents(); len(got) != 3 || got[0] != "one" || got[2] != "three again" {
		t.Errorf("branched conversation = %q, want one, two, three again", got)
	}
	if head, err := tree.Head(ctx, sess.ID); err != nil || head.ID != branch.ID {
		t.Errorf("Head = %+v, %v; want the new branch", head, err)
	}

	nodes, err := tree.Outline(ctx, sess.ID)
	if err != nil || len(nodes) != 6 {
		t.Fatalf("Outline = %+v, %v; want every entry of both branches", nodes, err)
	}
	if nodes[2].ParentID != before.ID || nodes[5].ParentID != before.ID {
		t.Errorf("outline parents = %q, %q; want both branches under two", nodes[2].ParentID, nodes[5].ParentID)
	}
	if nodes[3].Kind != store.KindCompaction || !nodes[3].Resumable {
		t.Errorf("compaction node = %+v, want a resumable compaction", nodes[3])
	}
}
