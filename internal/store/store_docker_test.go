//go:build docker

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
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

func TestComputersAndSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "fix-build", Image: "eika-sandbox:latest", State: "creating",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	if err := st.SetComputerState(ctx, cx.ID, "running", "container-1"); err != nil {
		t.Fatalf("set computer state: %v", err)
	}
	got, err := st.Computer(ctx, cx.ID)
	if err != nil {
		t.Fatalf("read computer: %v", err)
	}
	if got.State != "running" || got.ContainerID != "container-1" {
		t.Errorf("computer = %+v, want running container-1", got)
	}
	if err := st.SetComputerState(ctx, cx.ID, "stopped", ""); err != nil {
		t.Fatalf("set computer state: %v", err)
	}
	if got, err = st.Computer(ctx, cx.ID); err != nil || got.ContainerID != "container-1" {
		t.Errorf("empty container id overwrote the recorded one: %+v, %v", got, err)
	}
	if !reflect.DeepEqual(got.Sandbox, store.ComputerSandbox{}) {
		t.Errorf("a new computer's sandbox = %+v, want none of its fields set", got.Sandbox)
	}
	sandbox := store.ComputerSandbox{
		CPUs: 1.5, MemoryMB: 2048, PIDs: 512, Egress: "allowlist",
		Allow: []string{"github.com"},
	}
	if got, err = st.SetComputerSandbox(ctx, cx.ID, sandbox); err != nil || !reflect.DeepEqual(got.Sandbox, sandbox) {
		t.Errorf("SetComputerSandbox = %+v, %v; want the sandbox back", got.Sandbox, err)
	}
	if got, err = st.Computer(ctx, cx.ID); err != nil || !reflect.DeepEqual(got.Sandbox, sandbox) {
		t.Errorf("computer sandbox = %+v, %v; want what was set", got.Sandbox, err)
	}
	if _, err := st.SetComputerSandbox(ctx, "missing", sandbox); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetComputerSandbox on a missing computer = %v, want ErrNotFound", err)
	}

	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "first"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	renamed := "renamed"
	if _, err := st.UpdateSession(ctx, sess.ID, store.SessionChanges{Title: &renamed}); err != nil {
		t.Fatalf("rename session: %v", err)
	}
	sessions, err := st.Sessions(ctx, cx.ID, false)
	if err != nil || len(sessions) != 1 || sessions[0].Title != "renamed" {
		t.Fatalf("Sessions = %+v, %v", sessions, err)
	}
	if sessions[0].Kind != store.SessionUser {
		t.Errorf("kind = %q, want %q", sessions[0].Kind, store.SessionUser)
	}
}

// A fork with a computer runs somewhere else, so the listing of the
// computer it came from has to reach it: the user interface draws it under
// the session that led to it.
func TestSessionsReachDescendantsInOtherComputers(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	parentWS, err := st.CreateComputer(ctx, store.Computer{
		Name: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	childWS, err := st.CreateComputer(ctx, store.Computer{
		Name:  "fork",
		State: "running",
	})
	if err != nil {
		t.Fatalf("create child computer: %v", err)
	}

	parent, err := st.CreateSession(ctx, store.Session{ComputerID: parentWS.ID, Title: "parent"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	child, err := st.CreateSession(ctx, store.Session{
		ComputerID: childWS.ID, Title: "child", Kind: store.SessionFork,
		ParentSessionID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child session: %v", err)
	}
	grand, err := st.CreateSession(ctx, store.Session{
		ComputerID: childWS.ID, Title: "grandchild", Kind: store.SessionFork,
		ParentSessionID: child.ID,
	})
	if err != nil {
		t.Fatalf("create grandchild session: %v", err)
	}

	flat, err := st.Sessions(ctx, parentWS.ID, false)
	if err != nil || len(flat) != 1 || flat[0].ID != parent.ID {
		t.Fatalf("Sessions without descendants = %+v, %v, want the parent alone", flat, err)
	}
	tree, err := st.Sessions(ctx, parentWS.ID, true)
	if err != nil {
		t.Fatalf("Sessions with descendants: %v", err)
	}
	got := map[string]store.SessionKind{}
	for _, sess := range tree {
		got[sess.ID] = sess.Kind
	}
	want := map[string]store.SessionKind{
		parent.ID: store.SessionUser,
		child.ID:  store.SessionFork,
		grand.ID:  store.SessionFork,
	}
	if len(got) != len(want) {
		t.Fatalf("Sessions with descendants = %+v, want %d rows", tree, len(want))
	}
	for id, kind := range want {
		if got[id] != kind {
			t.Errorf("session %s kind = %q, want %q", id, got[id], kind)
		}
	}
}

// A chat is a session with no computer. It is listed apart from every
// computer's sessions, its fork is a chat too, and both keep the tools the
// user chose.
func TestChatsHaveNoComputerAndKeepTheirTools(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	if _, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "work"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	chat, err := st.CreateSession(ctx, store.Session{Title: "question"})
	if err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if !chat.Chat() || chat.ComputerID != "" {
		t.Errorf("chat = %+v, want no computer", chat)
	}
	if chat.Tools != nil {
		t.Errorf("new chat tools = %#v, want nil for every tool", chat.Tools)
	}

	if err := st.SetSessionTools(ctx, chat.ID, []string{"web_search"}); err != nil {
		t.Fatalf("set tools: %v", err)
	}
	entry, err := st.AppendEntry(ctx, chat.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("append entry: %v", err)
	}
	fork, err := st.ForkSession(ctx, chat.ID, entry.ID, store.ForkOptions{})
	if err != nil {
		t.Fatalf("fork chat: %v", err)
	}
	if !fork.Chat() || len(fork.Tools) != 1 || fork.Tools[0] != "web_search" {
		t.Errorf("fork = %+v, want a chat with web_search", fork)
	}

	chats, err := st.Chats(ctx)
	if err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if len(chats) != 2 || chats[0].ID != chat.ID || chats[1].ID != fork.ID {
		t.Fatalf("Chats = %+v, want the chat and its fork", chats)
	}
	if all, err := st.Sessions(ctx, "", false); err != nil || len(all) != 3 {
		t.Errorf("every session = %d rows, %v; want 3", len(all), err)
	}

	// An empty list is no tools, which is not the same as every tool.
	if err := st.SetSessionTools(ctx, chat.ID, []string{}); err != nil {
		t.Fatalf("clear tools: %v", err)
	}
	if got, err := st.Session(ctx, chat.ID); err != nil || got.Tools == nil || len(got.Tools) != 0 {
		t.Errorf("tools after clearing = %#v, %v; want empty, not nil", got.Tools, err)
	}
	if err := st.SetSessionTools(ctx, chat.ID, nil); err != nil {
		t.Fatalf("restore tools: %v", err)
	}
	if got, err := st.Session(ctx, chat.ID); err != nil || got.Tools != nil {
		t.Errorf("tools after restoring = %#v, %v; want nil", got.Tools, err)
	}
	if err := st.SetSessionTools(ctx, "missing", nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetSessionTools on a missing session = %v, want ErrNotFound", err)
	}
}

func TestRunsAndSettings(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "parent", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	parent, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID})
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
	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "work", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID})
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

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "w", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	missing := store.NewID()
	cases := map[string]error{
		"computer":   errOf(func() error { _, err := st.Computer(ctx, missing); return err }),
		"session":    errOf(func() error { _, err := st.Session(ctx, missing); return err }),
		"entry":      errOf(func() error { _, err := st.Entry(ctx, missing); return err }),
		"run":        errOf(func() error { _, err := st.Run(ctx, missing); return err }),
		"setting":    errOf(func() error { _, err := st.Setting(ctx, missing); return err }),
		"append":     errOf(func() error { _, err := st.AppendEntry(ctx, missing, store.Entry{Kind: store.KindUser}); return err }),
		"set head":   st.SetSessionHead(ctx, sess.ID, missing),
		"fork":       errOf(func() error { _, err := st.ForkSession(ctx, sess.ID, missing, store.ForkOptions{}); return err }),
		"finish run": st.FinishRun(ctx, missing, store.RunDone, ""),
		"delete":     st.DeleteComputer(ctx, missing),
		"update session": errOf(func() error {
			_, err := st.UpdateSession(ctx, missing, store.SessionChanges{})
			return err
		}),
		"update computer": errOf(func() error {
			_, err := st.UpdateComputer(ctx, missing, store.ComputerChanges{})
			return err
		}),
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

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "w", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID})
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

// errOf runs a call that only fails or does not, so that a table can hold it.
func errOf(call func() error) error { return call() }

// A title made in the background goes only to a session that is still
// untitled, and a fork that copies the placeholder stays untitled.
func TestUntitledSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	untitled, err := st.CreateSession(ctx, store.Session{Title: "New chat", Untitled: true})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if !untitled.Untitled {
		t.Fatalf("session = %+v, want untitled", untitled)
	}
	entry, err := st.AppendEntry(ctx, untitled.ID, store.Entry{
		Kind: store.KindUser, Payload: json.RawMessage(`{"role":"user","content":"one"}`),
	})
	if err != nil {
		t.Fatalf("append entry: %v", err)
	}
	fork, err := st.ForkSession(ctx, untitled.ID, entry.ID, store.ForkOptions{})
	if err != nil || !fork.Untitled || fork.Title != "New chat" {
		t.Errorf("fork copying the placeholder = %+v, %v; want untitled", fork, err)
	}
	named, err := st.ForkSession(ctx, untitled.ID, entry.ID, store.ForkOptions{Title: "Named"})
	if err != nil || named.Untitled {
		t.Errorf("fork with a title = %+v, %v; want titled", named, err)
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
	if _, err := st.UpdateSession(ctx, fork.ID, store.SessionChanges{Title: &chosen}); err != nil {
		t.Fatalf("rename session: %v", err)
	}
	if ok, err := st.TitleUntitledSession(ctx, fork.ID, "Generated"); err != nil || ok {
		t.Errorf("TitleUntitledSession after a rename = %v, %v; want the chosen title kept", ok, err)
	}
	if ok, err := st.TitleUntitledSession(ctx, "missing", "Generated"); err != nil || ok {
		t.Errorf("TitleUntitledSession on a missing session = %v, %v; want false", ok, err)
	}
}

// An edit changes the fields it names and leaves the rest, so renaming a
// session does not unpin it and archiving a computer does not rename it.
func TestUpdatesChangeOnlyTheFieldsTheyName(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	cx, err := st.CreateComputer(ctx, store.Computer{
		Name: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "New session", Untitled: true})
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
	if listed, err := st.Sessions(ctx, cx.ID, true); err != nil || len(listed) != 1 || listed[0].Title != "Named" {
		t.Errorf("Sessions = %+v, %v; want the edited session", listed, err)
	}

	name := "renamed"
	w, err := st.UpdateComputer(ctx, cx.ID, store.ComputerChanges{Archived: &yes})
	if err != nil || !w.Archived || w.Pinned || w.Name != "main" {
		t.Fatalf("archive computer = %+v, %v; want archived and otherwise unchanged", w, err)
	}
	w, err = st.UpdateComputer(ctx, cx.ID, store.ComputerChanges{Name: &name, Pinned: &yes})
	if err != nil || !w.Archived || !w.Pinned || w.Name != "renamed" || w.State != "running" {
		t.Fatalf("rename and pin computer = %+v, %v", w, err)
	}
	if w, err = st.Computer(ctx, cx.ID); err != nil || !w.Pinned || !w.Archived || w.Name != "renamed" {
		t.Errorf("Computer = %+v, %v; want what was set", w, err)
	}
}

func TestComputersListInOrderOfCreation(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	if got, err := st.Computers(ctx); err != nil || len(got) != 0 {
		t.Fatalf("Computers of a new database = %+v, %v; want none", got, err)
	}
	var want []string
	for _, name := range []string{"first", "second", "third"} {
		cx, err := st.CreateComputer(ctx, store.Computer{Name: name, State: "running"})
		if err != nil {
			t.Fatalf("create computer %s: %v", name, err)
		}
		want = append(want, cx.ID)
	}
	got, err := st.Computers(ctx)
	if err != nil {
		t.Fatalf("Computers: %v", err)
	}
	var ids []string
	for _, cx := range got {
		ids = append(ids, cx.ID)
	}
	if !slices.Equal(ids, want) {
		t.Errorf("Computers = %v, want %v", ids, want)
	}
}

func TestDeletingAComputerDeletesItsSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	cx, err := st.CreateComputer(ctx, store.Computer{Name: "doomed", State: "running"})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "in it"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.DeleteComputer(ctx, cx.ID); err != nil {
		t.Fatalf("delete computer: %v", err)
	}
	if _, err := st.Session(ctx, sess.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session of a deleted computer = %v, want ErrNotFound", err)
	}
}
