//go:build docker

package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
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

func TestRemoteCredentialMigrationSanitizesLegacyURLs(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err = conn.Exec(ctx, `
		CREATE TABLE schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO schema_migrations (version) VALUES ('0001_init');
		CREATE TABLE projects (
			id text PRIMARY KEY,
			name text NOT NULL UNIQUE,
			kind text NOT NULL,
			remote_url text NOT NULL DEFAULT '',
			host_path text NOT NULL DEFAULT '',
			default_branch text NOT NULL DEFAULT 'main',
			created_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO projects (id, name, kind, remote_url) VALUES
			('legacy-userinfo', 'legacy-userinfo', 'remote', 'https://user:private@example.test/userinfo.git'),
			('legacy-query', 'legacy-query', 'remote', 'https://example.test/query.git?access_token=private'),
			('legacy-fragment', 'legacy-fragment', 'remote', 'https://example.test/fragment.git#private'),
			('legacy-public', 'legacy-public', 'remote', 'https://example.test/public.git'),
			('legacy-escaped', 'legacy-escaped', 'remote', 'https://example.test/repo%3Fversion.git');
	`)
	if err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("prepare legacy schema: %v", err)
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close legacy connection: %v", err)
	}

	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	t.Cleanup(st.Close)
	cases := []struct {
		id     string
		remote string
	}{
		{"legacy-userinfo", "https://example.test/userinfo.git"},
		{"legacy-query", "https://example.test/query.git"},
		{"legacy-fragment", "https://example.test/fragment.git"},
		{"legacy-public", "https://example.test/public.git"},
		{"legacy-escaped", "https://example.test/repo%3Fversion.git"},
	}
	for _, c := range cases {
		project, err := st.Project(ctx, c.id)
		if err != nil {
			t.Fatalf("read migrated project %s: %v", c.id, err)
		}
		if project.RemoteURL != c.remote {
			t.Errorf("%s remote_url = %q, want %q", c.id, project.RemoteURL, c.remote)
		}
		// The credentials an old URL carried are not moved anywhere: they are
		// entered again, and sealed, in the project's settings.
		if project.RemoteUsername != "" || project.RemotePassword != nil {
			t.Errorf("%s credentials = %q, %v, want none", c.id, project.RemoteUsername, project.RemotePassword)
		}
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

func TestSetSettingsWritesAllOrNothing(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	if err := st.SetSettings(ctx, map[string]json.RawMessage{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`)}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	// PostgreSQL refuses a NUL character in jsonb, so this batch fails partway.
	err := st.SetSettings(ctx, map[string]json.RawMessage{
		"a": json.RawMessage(`10`), "b": json.RawMessage(`"\u0000"`), "c": json.RawMessage(`30`),
	})
	if err == nil {
		t.Fatal("SetSettings with an unstorable value succeeded, want an error")
	}
	all, err := st.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	got := map[string]string{}
	for _, set := range all {
		got[set.Key] = string(set.Value)
	}
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Errorf("settings = %v, want the failed batch to have written nothing", got)
	}
}

func TestAbortRunningRunsClosesRowsFromAnEarlierProcess(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	project := newProject(t, st)
	ws, err := st.CreateWorkspace(ctx, store.Workspace{
		ProjectID: project.ID, Name: "work", Branch: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	run, err := st.StartRun(ctx, sess.ID)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	aborted, err := st.AbortRunningRuns(ctx)
	if err != nil {
		t.Fatalf("AbortRunningRuns: %v", err)
	}
	if aborted != 1 {
		t.Fatalf("aborted rows = %d, want 1", aborted)
	}
	got, err := st.Run(ctx, run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if got.State != store.RunAborted || got.FinishedAt.IsZero() {
		t.Errorf("run = %+v, want an aborted run with a finish time", got)
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
