package memory_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/memory/repo"
)

var errInjected = errors.New("injected failure")

// flakyFiles fails the step named in fail until it is cleared.
type flakyFiles struct {
	*repo.Repository
	failCommit bool
}

func (f *flakyFiles) Commit(ctx context.Context, name string) error {
	if f.failCommit {
		return errInjected
	}
	return f.Repository.Commit(ctx, name)
}

// flakyIndex fails upserts until failUpsert is cleared.
type flakyIndex struct {
	*fakeIndex
	failUpsert bool
}

func (f *flakyIndex) UpsertMemory(ctx context.Context, r memory.Record) error {
	if f.failUpsert {
		return errInjected
	}
	return f.fakeIndex.UpsertMemory(ctx, r)
}

type env struct {
	dir string
	r   *repo.Repository
	idx *flakyIndex
	fl  *flakyFiles
	w   *memory.Writer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	ex, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := repo.NewRepository(ex)
	if err := r.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := &env{dir: dir, r: r, idx: &flakyIndex{fakeIndex: &fakeIndex{rows: map[string]memory.Record{}}}, fl: &flakyFiles{Repository: r}}
	e.w = memory.NewWriter(e.fl, e.idx)
	return e
}

func (e *env) commits(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("git", "-C", e.dir, "rev-list", "--count", "--all").Output()
	if err != nil {
		return 0 // no commit yet
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func (e *env) edit(t *testing.T, name, before, after string) {
	t.Helper()
	path := filepath.Join(e.dir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), before) {
		t.Fatalf("edit %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), before, after, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) clean(t *testing.T) {
	t.Helper()
	if status, err := e.r.Status(t.Context()); err != nil || status != "" {
		t.Fatalf("uncommitted: %q, %v", status, err)
	}
}

// A failure after the file write, or after the commit, leaves the file ahead
// of the row; Reconcile after a restart repairs it, and again is a no-op.
func TestReconcileRepairsFailuresBetweenSteps(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t)

	e.idx.failUpsert = true
	if _, err := e.w.Write(ctx, request("after-commit", "kept")); !errors.Is(err, errInjected) {
		t.Fatalf("write = %v, want injected failure", err)
	}
	e.idx.failUpsert = false
	e.fl.failCommit = true
	if _, err := e.w.Write(ctx, request("before-commit", "cut short")); !errors.Is(err, errInjected) {
		t.Fatalf("write = %v, want injected failure", err)
	}
	e.fl.failCommit = false
	// A restart: a new writer over the same files and index.
	w := memory.NewWriter(e.r, e.idx)
	for range 2 { // the second run changes nothing
		rep, err := w.Reconcile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = rep
	}
	e.clean(t)
	if got := e.idx.rows["after-commit"]; got.State != "active" || got.Body != "kept" {
		t.Fatalf("committed write row = %+v, want active", got)
	}
	// An own write cut off before its commit cannot be told from an outside
	// edit, so it is held until the screen says otherwise.
	if got := e.idx.rows["before-commit"]; got.State != "pending" {
		t.Fatalf("uncommitted write row = %+v, want pending", got)
	}
	n := e.commits(t)
	if rep, err := w.Reconcile(ctx); err != nil || rep.Reindexed != 0 || rep.Committed != 0 || e.commits(t) != n {
		t.Fatalf("third reconcile = %+v, %v; want nothing to do", rep, err)
	}
}

func TestReconcileScreensOutsideEdits(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t)
	for _, n := range []string{"edited", "archived", "gone", "stale"} {
		if _, err := e.w.Write(ctx, request(n, "original")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.w.Archive(ctx, "archived"); err != nil {
		t.Fatal(err)
	}
	e.edit(t, "edited", "original", "changed outside")
	e.edit(t, "archived", "original", "changed outside")
	if err := os.Remove(filepath.Join(e.dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	// A row with no file behind it, and a file that is not a memory.
	e.idx.rows["orphan"] = memory.Record{File: memory.File{Name: "orphan"}, ContentSHA: "x"}
	if err := os.WriteFile(filepath.Join(e.dir, "junk.md"), []byte("not a memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var screened []string
	w := memory.NewWriter(e.r, e.idx)
	w.SetScreen(func(_ context.Context, f memory.File) (string, error) {
		screened = append(screened, f.Name)
		return "pending", nil
	})
	rep, err := w.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.idx.rows["edited"]; got.State != "pending" || got.Body != "changed outside" {
		t.Fatalf("edited row = %+v", got)
	}
	if f, _ := w.Read(ctx, "edited"); f.State != "pending" {
		t.Fatalf("edited file state = %q, want pending in the file too", f.State)
	}
	if got := e.idx.rows["archived"]; got.State != "archived" || got.Body != "changed outside" {
		t.Fatalf("archived row = %+v, want state kept", got)
	}
	if strings.Join(screened, ",") != "edited" {
		t.Fatalf("screened %v, want only the active edit", screened)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "gone.md")); err != nil {
		t.Fatalf("deleted file not restored: %v", err)
	}
	if _, ok := e.idx.rows["orphan"]; ok {
		t.Fatal("orphan row kept")
	}
	if _, ok := e.idx.rows["junk"]; ok || len(rep.Invalid) != 1 {
		t.Fatalf("invalid file indexed or unreported: %+v", rep)
	}
	if e.idx.rows["gone"].Body != "original" {
		t.Fatalf("restored row = %+v", e.idx.rows["gone"])
	}
	_ = os.Remove(filepath.Join(e.dir, "junk.md"))
	e.clean(t)
}

// A full rebuild from files alone keeps states and commits and screens
// nothing, because every file is committed.
func TestReindexRebuildsTheWholeIndex(t *testing.T) {
	ctx := t.Context()
	e := newEnv(t)
	for _, n := range []string{"one", "two", "three"} {
		if _, err := e.w.Write(ctx, request(n, "body "+n)); err != nil {
			t.Fatal(err)
		}
	}
	req := request("held", "held body")
	req.State = "pending"
	if _, err := e.w.Write(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := e.w.Archive(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	want := map[string]memory.Record{}
	for n, r := range e.idx.rows {
		want[n] = r
	}
	commits := e.commits(t)
	e.idx.rows = map[string]memory.Record{} // lose the table
	e.idx.rows["stale"] = memory.Record{ContentSHA: "x"}
	rep, err := memory.NewWriter(e.r, e.idx).Reindex(ctx)
	if err != nil || rep.Held != 0 || rep.Committed != 0 {
		t.Fatalf("reindex = %+v, %v", rep, err)
	}
	if len(e.idx.rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(e.idx.rows), len(want))
	}
	for n, r := range want {
		if e.idx.rows[n] != r {
			t.Errorf("row %s = %+v, want %+v", n, e.idx.rows[n], r)
		}
	}
	if e.commits(t) != commits {
		t.Fatal("reindex made commits")
	}
}
