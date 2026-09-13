//go:build docker

package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m))
}

// newProject creates a project to hang test rows off.
func newProject(t *testing.T, st *store.Store) store.Project {
	t.Helper()
	p, err := st.CreateProject(t.Context(), store.Project{
		Name:          "eika-" + store.NewID(),
		Kind:          store.ProjectRemote,
		RemoteURL:     "https://example.invalid/eika.git",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	url := storetest.URL(t)
	for _, pass := range []string{"first", "second"} {
		st, err := store.Open(t.Context(), url)
		if err != nil {
			t.Fatalf("%s open: %v", pass, err)
		}
		if _, err := st.CreateProject(t.Context(), store.Project{
			Name: pass, Kind: store.ProjectLocal, HostPath: "/srv/eika", DefaultBranch: "main",
		}); err != nil {
			t.Fatalf("%s open, create project: %v", pass, err)
		}
		st.Close()
	}
}

func TestProjectsWorkspacesAndSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	project := newProject(t, st)
	if got, err := st.ProjectByName(ctx, project.Name); err != nil || got.ID != project.ID {
		t.Fatalf("ProjectByName = %+v, %v", got, err)
	}
	if got, err := st.Projects(ctx); err != nil || len(got) != 1 {
		t.Fatalf("Projects = %d rows, %v; want 1", len(got), err)
	}

	ws, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "fix-build", Branch: "fix-build",
		BaseCommit: "0123456789abcdef", Image: "eika-sandbox:latest", State: "creating",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := st.SetWorkspaceState(ctx, ws.ID, "running", "container-1"); err != nil {
		t.Fatalf("set workspace state: %v", err)
	}
	got, err := st.Workspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if got.State != "running" || got.ContainerID != "container-1" {
		t.Errorf("workspace = %+v, want running container-1", got)
	}
	if err := st.SetWorkspaceState(ctx, ws.ID, "stopped", ""); err != nil {
		t.Fatalf("set workspace state: %v", err)
	}
	if got, err = st.Workspace(ctx, ws.ID); err != nil || got.ContainerID != "container-1" {
		t.Errorf("empty container id overwrote the recorded one: %+v, %v", got, err)
	}

	sess, err := st.CreateSession(ctx, store.Session{WorkspaceID: ws.ID, Title: "first"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.SetSessionTitle(ctx, sess.ID, "renamed"); err != nil {
		t.Fatalf("set session title: %v", err)
	}
	sessions, err := st.Sessions(ctx, ws.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Title != "renamed" {
		t.Fatalf("Sessions = %+v, %v", sessions, err)
	}
}

func TestRunsSubagentsAndSettings(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	project := newProject(t, st)
	ws, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "parent", Branch: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	parent, err := st.CreateSession(ctx, store.Session{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	run, err := st.StartRun(ctx, parent.ID)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if run.State != store.RunRunning || !run.FinishedAt.IsZero() {
		t.Errorf("run = %+v, want a running run without a finish time", run)
	}
	if err := st.FinishRun(ctx, run.ID, store.RunError, "provider refused"); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	finished, err := st.Run(ctx, run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if finished.State != store.RunError || finished.Error != "provider refused" || finished.FinishedAt.IsZero() {
		t.Errorf("run = %+v, want a failed run with a finish time", finished)
	}
	if runs, err := st.Runs(ctx, parent.ID); err != nil || len(runs) != 1 {
		t.Fatalf("Runs = %d rows, %v; want 1", len(runs), err)
	}

	childWS, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "child", Branch: "main/child", State: "running",
		ParentWorkspaceID: ws.ID,
	})
	if err != nil {
		t.Fatalf("create child workspace: %v", err)
	}
	child, err := st.CreateSession(ctx, store.Session{WorkspaceID: childWS.ID, ParentSessionID: parent.ID})
	if err != nil {
		t.Fatalf("create child session: %v", err)
	}
	sub, err := st.StartSubagent(ctx, store.Subagent{
		ParentSessionID: parent.ID, ChildSessionID: child.ID, ChildWorkspaceID: childWS.ID,
	})
	if err != nil {
		t.Fatalf("start subagent: %v", err)
	}
	if err := st.FinishSubagent(ctx, sub.ID, store.RunDone, "pushed main/child"); err != nil {
		t.Fatalf("finish subagent: %v", err)
	}
	subs, err := st.Subagents(ctx, parent.ID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("Subagents = %d rows, %v; want 1", len(subs), err)
	}
	if subs[0].Result != "pushed main/child" || subs[0].State != store.RunDone {
		t.Errorf("subagent = %+v, want a finished subagent with its result", subs[0])
	}

	if err := st.SetSetting(ctx, "model", json.RawMessage(`{"name":"gpt-5"}`)); err != nil {
		t.Fatalf("write setting: %v", err)
	}
	if err := st.SetSetting(ctx, "model", json.RawMessage(`{"name":"gpt-5-mini"}`)); err != nil {
		t.Fatalf("overwrite setting: %v", err)
	}
	set, err := st.Setting(ctx, "model")
	if err != nil {
		t.Fatalf("read setting: %v", err)
	}
	var value struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(set.Value, &value); err != nil || value.Name != "gpt-5-mini" {
		t.Fatalf("setting = %s, %v; want the second write", set.Value, err)
	}
	if all, err := st.Settings(ctx); err != nil || len(all) != 1 {
		t.Fatalf("Settings = %+v, %v; want one row", all, err)
	}
}

func TestErrNotFound(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	project := newProject(t, st)
	ws, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "w", Branch: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	missing := store.NewID()
	cases := map[string]error{
		"project":       errOf(func() error { _, err := st.Project(ctx, missing); return err }),
		"project name":  errOf(func() error { _, err := st.ProjectByName(ctx, missing); return err }),
		"workspace":     errOf(func() error { _, err := st.Workspace(ctx, missing); return err }),
		"session":       errOf(func() error { _, err := st.Session(ctx, missing); return err }),
		"entry":         errOf(func() error { _, err := st.Entry(ctx, missing); return err }),
		"run":           errOf(func() error { _, err := st.Run(ctx, missing); return err }),
		"subagent":      errOf(func() error { _, err := st.Subagent(ctx, missing); return err }),
		"setting":       errOf(func() error { _, err := st.Setting(ctx, missing); return err }),
		"append":        errOf(func() error { _, err := st.AppendEntry(ctx, missing, store.Entry{Kind: store.KindUser}); return err }),
		"set head":      st.SetSessionHead(ctx, sess.ID, missing),
		"fork":          errOf(func() error { _, err := st.ForkSession(ctx, sess.ID, missing, store.ForkOptions{}); return err }),
		"finish run":    st.FinishRun(ctx, missing, store.RunDone, ""),
		"delete":        st.DeleteProject(ctx, missing),
		"session title": st.SetSessionTitle(ctx, missing, "x"),
	}
	for name, err := range cases {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestSetSessionHeadIsGuarded(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	project := newProject(t, st)
	ws, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "w", Branch: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	entry, err := st.AppendEntry(ctx, sess.ID, store.Entry{
		Kind: store.KindUser, Payload: json.RawMessage(`{"content":"one"}`),
	})
	if err != nil {
		t.Fatalf("append entry: %v", err)
	}

	if err := st.SetSessionHead(ctx, sess.ID, ""); err != nil {
		t.Fatalf("clear head: %v", err)
	}
	cleared, err := st.Session(ctx, sess.ID)
	if err != nil || cleared.HeadEntryID != "" {
		t.Fatalf("session = %+v, %v; want a cleared head", cleared, err)
	}
	if entries, err := st.Entries(ctx, sess.ID); err != nil || len(entries) != 1 {
		t.Fatalf("Entries = %d, %v; want the entry to survive a cleared head", len(entries), err)
	}
	if err := st.SetSessionHead(ctx, sess.ID, entry.ID); err != nil {
		t.Fatalf("restore head: %v", err)
	}

	if err := st.SetSessionHead(ctx, store.NewID(), entry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("set head of a missing session = %v, want ErrNotFound", err)
	}
	if err := st.SetSessionHead(ctx, store.NewID(), ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("clear head of a missing session = %v, want ErrNotFound", err)
	}
}

func TestDuplicateProjectNameIsConflict(t *testing.T) {
	st := storetest.Open(t)
	project := newProject(t, st)
	_, err := st.CreateProject(t.Context(), store.Project{
		Name: project.Name, Kind: store.ProjectLocal, HostPath: "/srv", DefaultBranch: "main",
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// errOf runs a call that only fails or does not, so that a table can hold it.
func errOf(call func() error) error { return call() }
