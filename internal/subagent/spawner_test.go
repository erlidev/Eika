package subagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
)

// testLogger returns a logger that writes nowhere.
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// rows is a store holding the rows a spawner reads before it touches a
// workspace: the session it spawns from, and the subagents already there. A
// test that gets past those checks would need a Docker daemon, so these tests
// stop where the limits do.
type rows struct {
	mu        sync.Mutex
	sessions  map[string]store.Session
	subagents map[string]store.Subagent
	// bySession maps a child session to the subagent row it belongs to,
	// which is how depth is measured.
	bySession map[string]string
	// reached records the calls that mean a spawn got past the limits.
	reached []string
}

func newRows() *rows {
	return &rows{
		sessions:  map[string]store.Session{},
		subagents: map[string]store.Subagent{},
		bySession: map[string]string{},
	}
}

// session records a session in a workspace.
func (r *rows) session(id, workspaceID string) store.Session {
	sess := store.Session{ID: id, WorkspaceID: workspaceID}
	r.sessions[id] = sess
	return sess
}

// child records a finished or running child of a parent session.
func (r *rows) child(id, parentSessionID, childSessionID string, state store.RunState, result string) store.Subagent {
	row := store.Subagent{
		ID:               id,
		ParentSessionID:  parentSessionID,
		ChildSessionID:   childSessionID,
		ChildWorkspaceID: "ws-" + id,
		State:            state,
		Result:           result,
	}
	r.subagents[id] = row
	r.bySession[childSessionID] = id
	return row
}

func (r *rows) Session(_ context.Context, id string) (store.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess, ok := r.sessions[id]
	if !ok {
		return store.Session{}, fmt.Errorf("read session %s: %w", id, store.ErrNotFound)
	}
	return sess, nil
}

func (r *rows) SessionPath(context.Context, string) ([]store.Entry, error) { return nil, nil }

func (r *rows) CreateSession(_ context.Context, sess store.Session) (store.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reached = append(r.reached, "create session")
	sess.ID = store.NewID()
	r.sessions[sess.ID] = sess
	return sess, nil
}

func (r *rows) Workspace(_ context.Context, id string) (store.Workspace, error) {
	return store.Workspace{ID: id, ProjectID: "p1", Branch: "main"}, nil
}

func (r *rows) CreateWorkspace(_ context.Context, ws store.Workspace) (store.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reached = append(r.reached, "create workspace")
	return ws, nil
}

func (r *rows) DeleteWorkspace(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reached = append(r.reached, "delete workspace "+id)
	return nil
}

func (r *rows) SetWorkspaceState(context.Context, string, string, string) error { return nil }

func (r *rows) Project(_ context.Context, id string) (store.Project, error) {
	return store.Project{ID: id, Name: "demo", Kind: store.ProjectLocal, DefaultBranch: "main"}, nil
}

func (r *rows) StartSubagent(_ context.Context, sub store.Subagent) (store.Subagent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reached = append(r.reached, "start subagent")
	sub.ID = store.NewID()
	sub.State = store.RunRunning
	r.subagents[sub.ID] = sub
	return sub, nil
}

func (r *rows) FinishSubagent(context.Context, string, store.RunState, string) error { return nil }

func (r *rows) Subagent(_ context.Context, id string) (store.Subagent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.subagents[id]
	if !ok {
		return store.Subagent{}, fmt.Errorf("read subagent %s: %w", id, store.ErrNotFound)
	}
	return row, nil
}

func (r *rows) Subagents(_ context.Context, parentSessionID string) ([]store.Subagent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []store.Subagent
	for _, row := range r.subagents {
		if row.ParentSessionID == parentSessionID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *rows) SubagentOfSession(_ context.Context, childSessionID string) (store.Subagent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.bySession[childSessionID]
	if !ok {
		return store.Subagent{}, fmt.Errorf("read subagent of session %s: %w", childSessionID, store.ErrNotFound)
	}
	return r.subagents[id], nil
}

// runner stands in for the run manager. A spawner that reaches it has got
// past everything these tests are about.
type runner struct{}

func (runner) RunChild(context.Context, string, string, string) error { return nil }

// errReachedWorkspaces is what a spawn that got past the name and the limits
// fails with: everything after them needs a Docker daemon, which the
// docker-tagged tests supply and these do not.
var errReachedWorkspaces = errors.New("reached the workspace host")

// haltingHost is a workspace host that refuses the first call a spawn makes
// on it, so that a test can tell "the limits allowed this" from "the limits
// rejected this" without creating anything.
type haltingHost struct{ subagent.Workspaces }

func (haltingHost) Inspect(context.Context, string) (workspace.Workspace, error) {
	return workspace.Workspace{}, errReachedWorkspaces
}

// newSpawner returns a spawner on rows, with a runner attached unless the
// test asks for one without.
func newSpawner(t *testing.T, r *rows, depth, children int, attach bool) *subagent.Spawner {
	t.Helper()
	s := subagent.New(subagent.Options{
		Store:       r,
		Workspaces:  haltingHost{},
		MaxDepth:    depth,
		MaxChildren: children,
		Logger:      testLogger(),
	})
	if attach {
		s.Attach(runner{})
	}
	return s
}

func TestSpawnRejectsANameThatCannotBeABranch(t *testing.T) {
	r := newRows()
	r.session("s1", "ws1")
	s := newSpawner(t, r, 2, 4, true)

	for _, name := range []string{"", "fix the bug", "feature/fix", "-fix", "fix; rm -rf /"} {
		t.Run(name, func(t *testing.T) {
			_, err := s.Spawn(t.Context(), builtin.SpawnRequest{
				ParentSessionID: "s1", Name: name, Task: "do it",
			})
			if !errors.Is(err, subagent.ErrBadName) {
				t.Fatalf("Spawn(%q) = %v, want ErrBadName", name, err)
			}
		})
	}
	t.Run("a branch suffix of its own", func(t *testing.T) {
		_, err := s.Spawn(t.Context(), builtin.SpawnRequest{
			ParentSessionID: "s1", Name: "fix", BranchSuffix: "../escape", Task: "do it",
		})
		if !errors.Is(err, subagent.ErrBadName) {
			t.Fatalf("Spawn = %v, want ErrBadName", err)
		}
	})
	if len(r.reached) != 0 {
		t.Errorf("a rejected name still wrote rows: %v", r.reached)
	}
}

func TestSpawnNeedsARunner(t *testing.T) {
	r := newRows()
	r.session("s1", "ws1")
	s := newSpawner(t, r, 2, 4, false)

	_, err := s.Spawn(t.Context(), builtin.SpawnRequest{ParentSessionID: "s1", Name: "fix", Task: "do it"})
	if !errors.Is(err, subagent.ErrNoRunner) {
		t.Fatalf("Spawn = %v, want ErrNoRunner", err)
	}
}

func TestSpawnStopsAtTheDepthLimit(t *testing.T) {
	// A tree of sessions: the root spawned the child, the child spawned the
	// grandchild. With a depth limit of two the grandchild may spawn nothing.
	r := newRows()
	r.session("root", "ws0")
	r.session("child", "ws1")
	r.session("grandchild", "ws2")
	r.child("a1", "root", "child", store.RunDone, "")
	r.child("a2", "child", "grandchild", store.RunDone, "")

	cases := map[string]struct {
		session string
		depth   int
		ok      bool
	}{
		"the root, one level allowed":        {"root", 1, true},
		"a child, one level allowed":         {"child", 1, false},
		"a child, two levels allowed":        {"child", 2, true},
		"a grandchild, two levels allowed":   {"grandchild", 2, false},
		"a grandchild, three levels allowed": {"grandchild", 3, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newSpawner(t, r, c.depth, 4, true)
			_, err := s.Spawn(t.Context(), builtin.SpawnRequest{
				ParentSessionID: c.session, Name: "fix", Task: "do it",
			})
			if c.ok && !errors.Is(err, errReachedWorkspaces) {
				t.Fatalf("Spawn = %v, want it allowed through to the workspace host", err)
			}
			if !c.ok && !errors.Is(err, subagent.ErrTooDeep) {
				t.Fatalf("Spawn = %v, want ErrTooDeep", err)
			}
		})
	}
}

func TestSpawnStopsAtTheChildLimit(t *testing.T) {
	r := newRows()
	r.session("s1", "ws1")
	r.child("a1", "s1", "c1", store.RunRunning, "")
	r.child("a2", "s1", "c2", store.RunRunning, "")
	r.child("a3", "s1", "c3", store.RunDone, "")

	t.Run("under the limit", func(t *testing.T) {
		s := newSpawner(t, r, 2, 3, true)
		_, err := s.Spawn(t.Context(), builtin.SpawnRequest{ParentSessionID: "s1", Name: "fix", Task: "do it"})
		if !errors.Is(err, errReachedWorkspaces) {
			t.Fatalf("Spawn = %v, want it allowed: only two of the three children are running", err)
		}
	})
	t.Run("at the limit", func(t *testing.T) {
		s := newSpawner(t, r, 2, 2, true)
		_, err := s.Spawn(t.Context(), builtin.SpawnRequest{ParentSessionID: "s1", Name: "fix", Task: "do it"})
		if !errors.Is(err, subagent.ErrTooMany) {
			t.Fatalf("Spawn = %v, want ErrTooMany", err)
		}
	})
}

func TestListAndWaitReportWhatAChildRecorded(t *testing.T) {
	r := newRows()
	r.session("s1", "ws1")
	r.session("other", "ws9")
	stored := builtin.AgentResult{
		Name:     "fix",
		Branch:   "main-fix",
		Commit:   "abc123",
		Summary:  "changed one file",
		DiffStat: " notes.txt | 1 +",
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	r.child("a1", "s1", "c1", store.RunDone, string(encoded))
	r.child("a2", "other", "c2", store.RunDone, string(encoded))
	s := newSpawner(t, r, 2, 4, true)

	listed, err := s.List(t.Context(), "s1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List = %v, want the one child of s1", listed)
	}
	got := listed[0]
	if got.ID != "a1" || got.SessionID != "c1" || got.WorkspaceID != "ws-a1" || got.State != "done" {
		t.Errorf("result = %+v, want the row's own ids and state", got)
	}
	if got.Branch != "main-fix" || got.Commit != "abc123" || got.Summary != "changed one file" {
		t.Errorf("result = %+v, want what the child recorded", got)
	}
	report := got.Report()
	for _, want := range []string{"main-fix", "abc123", "changed one file", "notes.txt"} {
		if !strings.Contains(report, want) {
			t.Errorf("report = %q, want %q in it", report, want)
		}
	}

	waited, err := s.Wait(t.Context(), "s1", []string{"a1"})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(waited) != 1 || waited[0].Commit != "abc123" {
		t.Errorf("Wait = %+v, want the recorded result", waited)
	}
	if _, err := s.Wait(t.Context(), "s1", []string{"a2"}); !errors.Is(err, subagent.ErrNotChild) {
		t.Errorf("waiting for another session's child = %v, want ErrNotChild", err)
	}
}
