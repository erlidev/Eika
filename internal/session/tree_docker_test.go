//go:build docker

package session_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m))
}

// newTree returns a tree and an empty session in a workspace of its own.
func newTree(t *testing.T) (*session.Tree, store.Session) {
	t.Helper()
	st := storetest.Open(t)
	ctx := t.Context()
	project, err := st.CreateProject(ctx, store.Project{
		Name: "eika", Kind: store.ProjectRemote, DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ws, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "fix-build", Branch: "fix-build", State: "running",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{WorkspaceID: ws.ID, Title: "fix the build"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return session.NewTree(st), sess
}

// appendText appends a message of the given role and returns the entry.
func appendText(t *testing.T, tree *session.Tree, sessionID string, m provider.Message, commit string) store.Entry {
	t.Helper()
	e, err := tree.AppendMessage(t.Context(), sessionID, m, commit)
	if err != nil {
		t.Fatalf("append %s: %v", m.Role, err)
	}
	return e
}

// texts renders a path as the message contents it carries.
func texts(t *testing.T, entries []store.Entry) []string {
	t.Helper()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		m, ok, err := session.Message(e)
		if err != nil {
			t.Fatalf("decode entry: %v", err)
		}
		if ok {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestAppendBuildsPathAndMovesHead(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	if _, err := tree.Head(ctx, sess.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Head of an empty session = %v, want ErrNotFound", err)
	}
	if path, err := tree.Path(ctx, sess.ID); err != nil || len(path) != 0 {
		t.Fatalf("Path of an empty session = %d entries, %v", len(path), err)
	}

	first := appendText(t, tree, sess.ID, provider.UserMessage("fix the build"), "")
	second := appendText(t, tree, sess.ID, provider.AssistantMessage("on it", nil), "commit-1")

	if first.ParentID != "" {
		t.Errorf("root parent = %q, want empty", first.ParentID)
	}
	if second.ParentID != first.ID {
		t.Errorf("second parent = %q, want %q", second.ParentID, first.ID)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Errorf("seq = %d, %d; want 1, 2", first.Seq, second.Seq)
	}
	if second.Commit != "commit-1" {
		t.Errorf("commit = %q, want commit-1", second.Commit)
	}
	head, err := tree.Head(ctx, sess.ID)
	if err != nil || head.ID != second.ID {
		t.Fatalf("Head = %+v, %v; want the last entry", head, err)
	}
	path, err := tree.Path(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got := texts(t, path); len(got) != 2 || got[0] != "fix the build" || got[1] != "on it" {
		t.Errorf("Path = %q, want the two messages in order", got)
	}
}

func TestSetHeadBranchesInPlace(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	first := appendText(t, tree, sess.ID, provider.UserMessage("one"), "")
	appendText(t, tree, sess.ID, provider.AssistantMessage("two", nil), "")
	appendText(t, tree, sess.ID, provider.UserMessage("three"), "")

	if err := tree.SetHead(ctx, sess.ID, first.ID); err != nil {
		t.Fatalf("SetHead: %v", err)
	}
	branch := appendText(t, tree, sess.ID, provider.AssistantMessage("two again", nil), "")
	if branch.ParentID != first.ID {
		t.Errorf("branch parent = %q, want the entry the head was moved to", branch.ParentID)
	}
	if branch.Seq != 4 {
		t.Errorf("branch seq = %d, want 4: a branch keeps writing after the other entries", branch.Seq)
	}

	path, err := tree.Path(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got := texts(t, path); len(got) != 2 || got[0] != "one" || got[1] != "two again" {
		t.Errorf("Path = %q, want only the new branch", got)
	}

	children, err := tree.Children(ctx, sess.ID, first.ID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("Children = %d, want 2: the old continuation and the new one", len(children))
	}
	roots, err := tree.Children(ctx, sess.ID, "")
	if err != nil || len(roots) != 1 || roots[0].ID != first.ID {
		t.Fatalf("roots = %+v, %v; want the first entry", roots, err)
	}
}

func TestSetHeadRejectsAnotherSessionsEntry(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()
	entry := appendText(t, tree, sess.ID, provider.UserMessage("one"), "")

	other, err := tree.Fork(ctx, sess.ID, entry.ID, store.ForkOptions{})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if err := tree.SetHead(ctx, other.ID, entry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetHead across sessions = %v, want ErrNotFound", err)
	}
}

func TestForkCopiesThePathAndRunsOn(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	first := appendText(t, tree, sess.ID, provider.UserMessage("one"), "")
	second := appendText(t, tree, sess.ID, provider.AssistantMessage("two", nil), "commit-1")
	appendText(t, tree, sess.ID, provider.UserMessage("three"), "")

	fork, err := tree.Fork(ctx, sess.ID, second.ID, store.ForkOptions{Title: "what if"})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if fork.ID == sess.ID || fork.ParentSessionID != sess.ID {
		t.Errorf("fork = %+v, want a new session whose parent is the original", fork)
	}
	if fork.Title != "what if" || fork.WorkspaceID != sess.WorkspaceID {
		t.Errorf("fork = %+v, want the given title and the original workspace", fork)
	}

	path, err := tree.Path(ctx, fork.ID)
	if err != nil {
		t.Fatalf("Path of the fork: %v", err)
	}
	if got := texts(t, path); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("fork path = %q, want the path up to the fork entry", got)
	}
	if path[0].ID == first.ID || path[1].ID == second.ID {
		t.Error("the fork shares entry rows with its parent; it must copy them")
	}
	if path[1].Commit != "commit-1" {
		t.Errorf("fork entry commit = %q, want the copied commit", path[1].Commit)
	}

	appendText(t, tree, fork.ID, provider.UserMessage("three again"), "")
	if got := texts(t, mustPath(t, tree, fork.ID)); len(got) != 3 || got[2] != "three again" {
		t.Errorf("fork path = %q, want the fork to continue on its own", got)
	}
	if got := texts(t, mustPath(t, tree, sess.ID)); len(got) != 3 || got[2] != "three" {
		t.Errorf("original path = %q, want it untouched by the fork", got)
	}
}

func TestOutlineDescribesTheWholeTree(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	user := appendText(t, tree, sess.ID, provider.UserMessage("fix the build\nplease"), "")
	calls := appendText(t, tree, sess.ID, provider.AssistantMessage("", []provider.ToolCall{
		{ID: "call_1", Name: "read", Arguments: provider.ToolArguments(`{"path":"go.mod"}`)},
		{ID: "call_2", Name: "bash", Arguments: provider.ToolArguments(`{"command":"go build"}`)},
	}), "commit-1")
	appendText(t, tree, sess.ID, provider.ToolResultMessage("call_1", "module eika", false), "")
	if err := tree.SetHead(ctx, sess.ID, user.ID); err != nil {
		t.Fatalf("SetHead: %v", err)
	}
	appendText(t, tree, sess.ID, provider.AssistantMessage("let me look", nil), "")

	nodes, err := tree.Outline(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("Outline = %d nodes, want every entry of both branches", len(nodes))
	}
	if nodes[0].ParentID != "" || nodes[1].ParentID != user.ID || nodes[3].ParentID != user.ID {
		t.Errorf("outline parents = %q, %q, %q; want the branch shape",
			nodes[0].ParentID, nodes[1].ParentID, nodes[3].ParentID)
	}
	if nodes[0].Preview != "fix the build ..." {
		t.Errorf("user preview = %q, want the first line", nodes[0].Preview)
	}
	if nodes[1].Preview != "calls read, bash" {
		t.Errorf("tool call preview = %q, want the tool names", nodes[1].Preview)
	}
	if nodes[1].Kind != store.KindAssistant || nodes[1].Commit != "commit-1" || nodes[1].ID != calls.ID {
		t.Errorf("node = %+v, want the assistant entry with its commit", nodes[1])
	}
	if nodes[0].CreatedAt.IsZero() || nodes[0].CreatedAt.Location().String() != "UTC" {
		t.Errorf("created_at = %v, want a UTC timestamp", nodes[0].CreatedAt)
	}
}

func TestStoreRecordsAndLoadsEveryMessageKind(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	commits := map[string]string{sess.ID: "commit-42"}
	st := session.NewStore(tree, func(_ context.Context, sessionID string) (string, error) {
		return commits[sessionID], nil
	})

	messages := []provider.Message{
		provider.UserMessage("fix the build"),
		provider.AssistantMessage("looking", []provider.ToolCall{
			{ID: "call_1", Name: "read", Arguments: provider.ToolArguments(`{"path":"go.mod","limit":20}`)},
			{ID: "call_2", Name: "bash", Arguments: provider.ToolArguments(`{"command":"go build ./..."}`)},
		}),
		provider.ToolResultMessage("call_1", "module github.com/erlidev/eika", false),
		provider.ToolResultMessage("call_2", "exit status 1", true),
		provider.AssistantMessage("fixed it", nil),
	}
	for _, m := range messages {
		if err := st.Append(ctx, sess.ID, m); err != nil {
			t.Fatalf("append %s: %v", m.Role, err)
		}
	}

	loaded, err := st.Load(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != sess.ID || loaded.WorkspaceID != sess.WorkspaceID {
		t.Errorf("loaded session = %+v, want the stored ids", loaded)
	}
	got := loaded.Conversation.Messages()
	if len(got) != len(messages) {
		t.Fatalf("loaded %d messages, want %d", len(got), len(messages))
	}
	for i, want := range messages {
		if !sameMessage(got[i], want) {
			t.Errorf("message %d = %+v, want %+v", i, got[i], want)
		}
	}

	path, err := tree.Path(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	for _, e := range path {
		switch e.Kind {
		case store.KindAssistant:
			if e.Commit != "commit-42" {
				t.Errorf("assistant entry commit = %q, want the workspace commit", e.Commit)
			}
		default:
			if e.Commit != "" {
				t.Errorf("%s entry commit = %q, want none", e.Kind, e.Commit)
			}
		}
	}
}

func TestLoadFollowsTheHead(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()
	st := session.NewStore(tree, nil)

	first := appendText(t, tree, sess.ID, provider.UserMessage("one"), "")
	appendText(t, tree, sess.ID, provider.AssistantMessage("two", nil), "")
	if err := tree.SetHead(ctx, sess.ID, first.ID); err != nil {
		t.Fatalf("SetHead: %v", err)
	}

	loaded, err := st.Load(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Conversation.Messages(); len(got) != 1 || got[0].Content != "one" {
		t.Errorf("loaded conversation = %+v, want the branch the head points at", got)
	}
	if _, err := st.Load(ctx, store.NewID()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Load of a missing session = %v, want ErrNotFound", err)
	}
}

func TestConcurrentAppendsKeepOneChain(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tree.AppendMessage(ctx, sess.ID, provider.UserMessage(strconv.Itoa(i)), "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}

	entries, err := tree.Outline(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if len(entries) != writers {
		t.Fatalf("Outline = %d nodes, want %d", len(entries), writers)
	}
	// Every append takes the session row lock, so the sequence numbers are
	// dense and unique and the entries form one chain, whatever order the
	// writers ran in.
	path, err := tree.Path(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if len(path) != writers {
		t.Fatalf("Path = %d entries, want one chain of %d", len(path), writers)
	}
	parent := ""
	for i, e := range path {
		if e.Seq != int64(i+1) {
			t.Errorf("entry %d has seq %d, want %d", i, e.Seq, i+1)
		}
		if e.ParentID != parent {
			t.Errorf("entry %d has parent %q, want %q", i, e.ParentID, parent)
		}
		parent = e.ID
	}
}

func TestArgumentLessToolCallSurvivesTheDatabase(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()

	call := provider.AssistantMessage("", []provider.ToolCall{{ID: "call_1", Name: "ls"}})
	if _, err := tree.AppendMessage(ctx, sess.ID, call, ""); err != nil {
		t.Fatalf("append a call without arguments: %v", err)
	}
	messages, err := tree.Messages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].ToolCalls) != 1 {
		t.Fatalf("Messages = %+v, want the one call", messages)
	}
	if got := string(messages[0].ToolCalls[0].Arguments); got != "{}" {
		t.Errorf("arguments = %q, want an empty object", got)
	}
}

func TestToolArgumentTextSurvivesTheDatabase(t *testing.T) {
	tree, sess := newTree(t)
	ctx := t.Context()
	want := []provider.ToolCall{
		{ID: "valid", Name: "accept_string", Arguments: provider.ToolArguments(`"value"`)},
		{ID: "malformed", Name: "read", Arguments: provider.ToolArguments(`{"path":`)},
	}
	if _, err := tree.AppendMessage(ctx, sess.ID, provider.AssistantMessage("", want), ""); err != nil {
		t.Fatalf("append calls: %v", err)
	}
	messages, err := tree.Messages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].ToolCalls) != len(want) {
		t.Fatalf("Messages = %+v, want both calls", messages)
	}
	for i, call := range messages[0].ToolCalls {
		if call.Arguments != want[i].Arguments {
			t.Errorf("call %s arguments = %q, want exact text %q", call.ID, call.Arguments, want[i].Arguments)
		}
	}
}

// mustPath reads a session's path or fails the test.
func mustPath(t *testing.T, tree *session.Tree, sessionID string) []store.Entry {
	t.Helper()
	path, err := tree.Path(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	return path
}
