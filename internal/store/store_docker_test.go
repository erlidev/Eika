//go:build docker

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m))
}

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	url := storetest.URL(t)
	for _, pass := range []string{"first", "second"} {
		st, err := store.Open(t.Context(), url)
		if err != nil {
			t.Fatalf("%s open: %v", pass, err)
		}
		if _, err := st.CreateComputer(t.Context(), store.Computer{Name: pass, State: "running"}); err != nil {
			t.Fatalf("%s open, create computer: %v", pass, err)
		}
		st.Close()
	}
}

func TestTablesLiveInSchemaCore(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st.Close()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var core, public int
	err = conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE table_schema = 'core'),
		count(*) FILTER (WHERE table_schema = 'public')
		FROM information_schema.tables WHERE table_type = 'BASE TABLE'`).Scan(&core, &public)
	if err != nil {
		t.Fatalf("count tables: %v", err)
	}
	// Sentry's schema sits beside core, so nothing may land in public.
	if core == 0 || public != 0 {
		t.Errorf("tables: %d in core, %d in public; want all in core", core, public)
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
	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "work", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", ComputerID: cx.ID})
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

func TestSetSessionHeadIsGuarded(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "w", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", ComputerID: cx.ID})
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

	// The head of a session never points at another session's entry.
	other, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", Title: "another"})
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if err := st.SetSessionHead(ctx, other.ID, entry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("set head to another session's entry = %v, want ErrNotFound", err)
	}
	if err := st.SetSessionHead(ctx, store.NewID(), entry.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("set head of a missing session = %v, want ErrNotFound", err)
	}
	if err := st.SetSessionHead(ctx, store.NewID(), ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("clear head of a missing session = %v, want ErrNotFound", err)
	}
}

// Every append takes the session row lock, so concurrent writers leave one
// chain with dense sequence numbers, whatever order they ran in.
func TestConcurrentAppendsKeepOneChain(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	sess, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", Title: "busy"})
	if err != nil {
		t.Fatal(err)
	}
	const writers = 20
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			if _, err := st.AppendEntry(ctx, sess.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"content":"x"}`)}); err != nil {
				t.Errorf("concurrent append: %v", err)
			}
		})
	}
	wg.Wait()
	path, err := st.SessionPath(ctx, sess.ID)
	if err != nil || len(path) != writers {
		t.Fatalf("SessionPath = %d entries, %v; want one chain of %d", len(path), err, writers)
	}
	parent := ""
	for i, e := range path {
		if e.Seq != int64(i+1) || e.ParentID != parent {
			t.Errorf("entry %d: seq %d parent %q, want %d %q", i, e.Seq, e.ParentID, i+1, parent)
		}
		parent = e.ID
	}
}

// A title made in the background goes only to a session that is still
// untitled.
func TestUntitledSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	untitled, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", Title: "New chat", Untitled: true})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if !untitled.Untitled {
		t.Fatalf("session = %+v, want untitled", untitled)
	}
	renamed, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", Title: "New chat", Untitled: true})
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	if ok, err := st.TitleUntitledSession(ctx, untitled.ID, "Generated"); err != nil || !ok {
		t.Fatalf("TitleUntitledSession = %v, %v; want it titled", ok, err)
	}
	got, err := st.Session(ctx, untitled.ID)
	if err != nil || got.Title != "Generated" || got.Untitled {
		t.Fatalf("session = %+v, %v; want titled Generated", got, err)
	}
	if ok, err := st.TitleUntitledSession(ctx, untitled.ID, "Again"); err != nil || ok {
		t.Errorf("second TitleUntitledSession = %v, %v; want the first title kept", ok, err)
	}

	// A title someone set is final, even over the placeholder.
	chosen := "Chosen"
	if _, err := st.UpdateSession(ctx, renamed.ID, store.SessionChanges{Title: &chosen}); err != nil {
		t.Fatalf("rename session: %v", err)
	}
	if ok, err := st.TitleUntitledSession(ctx, renamed.ID, "Generated"); err != nil || ok {
		t.Errorf("TitleUntitledSession after a rename = %v, %v; want the chosen title kept", ok, err)
	}
	if ok, err := st.TitleUntitledSession(ctx, "missing", "Generated"); err != nil || ok {
		t.Errorf("TitleUntitledSession on a missing session = %v, %v; want false", ok, err)
	}
}

// An edit changes the fields it names and leaves the rest, so renaming a
// session does not unpin it.
func TestUpdatesChangeOnlyTheFieldsTheyName(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "test", ComputerID: cx.ID, Title: "New session", Untitled: true})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	yes, no := true, false

	got, err := st.UpdateSession(ctx, sess.ID, store.SessionChanges{Pinned: &yes})
	if err != nil || !got.Pinned || got.Archived || got.Title != "New session" || !got.Untitled {
		t.Fatalf("pin = %+v, %v; want pinned and otherwise unchanged", got, err)
	}
	title := "Named"
	got, err = st.UpdateSession(ctx, sess.ID, store.SessionChanges{Title: &title, Archived: &yes})
	if err != nil || !got.Pinned || !got.Archived || got.Title != "Named" || got.Untitled {
		t.Fatalf("rename and archive = %+v, %v; want renamed, titled, archived, still pinned", got, err)
	}
	got, err = st.UpdateSession(ctx, sess.ID, store.SessionChanges{Pinned: &no, Archived: &no})
	if err != nil || got.Pinned || got.Archived || got.Title != "Named" {
		t.Fatalf("unpin and unarchive = %+v, %v", got, err)
	}
	if listed, err := st.Sessions(ctx, cx.ID); err != nil || len(listed) != 1 || listed[0].Title != "Named" {
		t.Errorf("Sessions = %+v, %v; want the edited session", listed, err)
	}
}

// upgradeFrom builds a database as migration last left it, with the
// migrations up to it recorded, runs setup on it, and opens the store over it,
// which applies every migration after. The connection stays open for checks
// the store has no call for.
func upgradeFrom(t *testing.T, last string, setup ...string) (*store.Store, *pgx.Conn) {
	t.Helper()
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
	statements := []string{
		`CREATE SCHEMA core`, `SET search_path = core`,
		`CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
	}
	files, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range files {
		name := strings.TrimSuffix(file.Name(), ".sql")
		if name > last {
			break
		}
		sql, err := os.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		statements = append(statements, string(sql), `INSERT INTO schema_migrations (version) VALUES ('`+name+`')`)
		found = found || name == last
	}
	if !found {
		t.Fatalf("no migration %s", last)
	}
	for _, stmt := range append(statements, setup...) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the %s database: %v\n%s", last, err, stmt)
		}
	}
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return st, conn
}

// A database at 0002 holds computers with the filing columns. The upgrade
// drops the columns and keeps every computer.
func TestUpgradeFrom0002KeepsComputersWithoutFiling(t *testing.T) {
	ctx := t.Context()
	st, conn := upgradeFrom(t, "0002_remove_session_forks",
		`INSERT INTO computers (id, name, state, pinned, archived, home) VALUES ('filed', 'filed', 'running', true, true, false)`,
		`INSERT INTO computers (id, name, state, home) VALUES ('home', 'home', 'running', true)`,
	)
	all, err := st.Computers(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("Computers = %+v, %v; want both computers kept", all, err)
	}
	if home, err := st.HomeComputer(ctx); err != nil || home.ID != "home" {
		t.Errorf("HomeComputer = %+v, %v; want the home computer", home, err)
	}
	var columns int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'computers' AND column_name IN ('pinned', 'archived')`).Scan(&columns)
	if err != nil || columns != 0 {
		t.Errorf("filing columns = %d, %v; want none", columns, err)
	}
}

// A database made by the baseline holds forks, which are sessions of their own
// once forking is gone. The upgrade keeps their history as user sessions,
// removes the fork relationship, and adds a new delegation relationship.
func TestUpgradeFromTheBaselineKeepsForksAsSessions(t *testing.T) {
	ctx := t.Context()
	st, conn := upgradeFrom(t, "0001_baseline",
		`INSERT INTO sessions (id, title) VALUES ('parent', 'parent')`,
		`INSERT INTO sessions (id, title, kind, parent_session_id) VALUES ('child', 'child', 'fork', 'parent')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
			('entry', 'child', 1, 'user', '{"role":"user","content":"saved"}')`,
		`UPDATE sessions SET head_entry_id = 'entry' WHERE id = 'child'`,
	)
	child, err := st.Session(ctx, "child")
	if err != nil || child.Kind != store.SessionRoutine || child.Title != "child" || child.ParentSessionID != "" || child.HeadEntryID != "entry" {
		t.Errorf("former fork = %+v, %v; want a routine session", child, err)
	}
	parent, err := st.Session(ctx, "parent")
	if err != nil || parent.Kind != store.SessionRoutine || parent.Title != "parent" || parent.ParentSessionID != "" {
		t.Errorf("parent = %+v, %v; want the original user session", parent, err)
	}
	path, err := st.SessionPath(ctx, "child")
	if err != nil || len(path) != 1 || path[0].ID != "entry" || path[0].Kind != store.KindUser {
		t.Fatalf("former fork path = %+v, %v; want its saved entry", path, err)
	}
	var message struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(path[0].Payload, &message); err != nil || message.Content != "saved" {
		t.Errorf("former fork entry = %s, %v; want saved content", path[0].Payload, err)
	}
	var oldParent *string
	if err := conn.QueryRow(ctx, `SELECT parent_session_id FROM sessions WHERE id = 'child'`).Scan(&oldParent); err != nil || oldParent != nil {
		t.Errorf("former fork parent = %v, %v; want SQL NULL", oldParent, err)
	}

	home, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Agent: "eika", Name: "home"})
	if err != nil {
		t.Fatalf("create home: %v", err)
	}
	delegated, err := st.CreateSession(ctx, store.Session{
		Kind: store.SessionSubagent, Agent: "worker", Name: "worker", ParentSessionID: home.ID,
	})
	if err != nil || delegated.ParentSessionID != home.ID {
		t.Fatalf("create delegated session = %+v, %v", delegated, err)
	}
	read, err := st.Session(ctx, delegated.ID)
	if err != nil || read.ParentSessionID != home.ID {
		t.Errorf("read delegated session = %+v, %v; want its delegating home", read, err)
	}
	for _, tc := range []struct {
		name, query, code string
	}{
		{"fork kind", `INSERT INTO sessions (id, kind, agent, name, parent_session_id) VALUES ('forked', 'fork', 'worker', 'forked', 'parent')`, "23514"},
		{"subagent without parent", `INSERT INTO sessions (id, kind, agent, name) VALUES ('orphan', 'subagent', 'worker', 'orphan')`, "23514"},
		{"subagent with missing parent", `INSERT INTO sessions (id, kind, agent, name, parent_session_id) VALUES ('missing', 'subagent', 'worker', 'missing', 'absent')`, "23503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := conn.Exec(ctx, tc.query)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
				t.Errorf("insert = %v; want PostgreSQL error %s", err, tc.code)
			}
		})
	}
}

func TestScreenSettingsAuditInSameTransaction(t *testing.T) {
	ctx := t.Context()
	url := storetest.URL(t)
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSettings(ctx, map[string]json.RawMessage{"screen_required": json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(ctx, map[string]json.RawMessage{"screen_required": json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM core.audit WHERE event = 'setting' AND name = 'screen_required'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit rows %d: %v", count, err)
	}
	err = st.SetSettings(ctx, map[string]json.RawMessage{"screen_required": json.RawMessage(`false`), "bad": json.RawMessage(`"\u0000"`)})
	if err == nil {
		t.Fatal("invalid setting accepted")
	}
	row, err := st.Setting(ctx, "screen_required")
	if err != nil || string(row.Value) != "true" {
		t.Fatalf("setting %s: %v", row.Value, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM core.audit WHERE event = 'setting' AND name = 'screen_required'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit rows %d: %v", count, err)
	}
}

func TestSettingsReadOneSnapshot(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	set := func(required bool) error {
		values := map[string]json.RawMessage{"screen_required": json.RawMessage(`false`), "screen_threshold": json.RawMessage(`0.2`)}
		if required {
			values["screen_required"], values["screen_threshold"] = json.RawMessage(`true`), json.RawMessage(`0.8`)
		}
		return st.SetSettings(ctx, values)
	}
	if err := set(false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := range 100 {
			if err := set(i%2 == 0); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 100 {
		rows, err := st.Settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var required, threshold string
		for _, row := range rows {
			if row.Key == "screen_required" {
				required = string(row.Value)
			}
			if row.Key == "screen_threshold" {
				threshold = string(row.Value)
			}
		}
		if (required == "true" && threshold != "0.8") || (required == "false" && threshold != "0.2") || (required != "true" && required != "false") {
			t.Fatalf("inconsistent settings: %s %s", required, threshold)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
