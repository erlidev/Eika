package memory_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/memory/repo"
)

// fakeIndex records upserts so a test can compare the index with the files.
type fakeIndex struct {
	mu   sync.Mutex
	rows map[string]memory.Record
}

func (f *fakeIndex) UpsertMemory(_ context.Context, r memory.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[r.Name] = r
	return nil
}

func (f *fakeIndex) MemorySHAs(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for n, r := range f.rows {
		out[n] = r.ContentSHA
	}
	return out, nil
}

func (f *fakeIndex) DeleteMemory(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, name)
	return nil
}

func (f *fakeIndex) SearchMemories(context.Context, memory.Query, int) ([]memory.Hit, error) {
	return nil, nil
}

func newWriter(t *testing.T) (*memory.Writer, *repo.Repository, *fakeIndex) {
	t.Helper()
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := repo.NewRepository(ex)
	if err := r.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	idx := &fakeIndex{rows: map[string]memory.Record{}}
	return memory.NewWriter(r, idx), r, idx
}

func request(name, body string) memory.Request {
	return memory.Request{
		Name: name, Description: "Use when the user asks about measurements.",
		Type: "preference", Body: body, Source: "session:s1 entry:e1",
	}
}

func TestConcurrentWritesAreSerialised(t *testing.T) {
	ctx := t.Context()
	w, r, idx := newWriter(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 20 {
		wg.Add(2)
		go func() { // distinct names
			defer wg.Done()
			_, err := w.Write(ctx, request(fmt.Sprintf("note-%d", i), "body"))
			errs <- err
		}()
		go func() { // one name, replaced by everyone
			defer wg.Done()
			req := request("shared", fmt.Sprintf("version %d", i))
			req.Replace = true
			_, err := w.Write(ctx, req)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if status, err := r.Status(ctx); err != nil || status != "" {
		t.Fatalf("uncommitted changes after writes: %q, %v", status, err)
	}
	if len(idx.rows) != 21 {
		t.Fatalf("index has %d rows, want 21", len(idx.rows))
	}
	got, err := w.Read(ctx, "shared")
	if err != nil || got.Body != idx.rows["shared"].Body {
		t.Fatalf("file %q and row %q disagree (%v)", got.Body, idx.rows["shared"].Body, err)
	}
}

func TestNameCollisionReturnsExistingUnlessReplaced(t *testing.T) {
	ctx := t.Context()
	w, _, idx := newWriter(t)
	first, err := w.Write(ctx, request("metric", "first"))
	if err != nil || first.Existing {
		t.Fatalf("first write: %+v, %v", first, err)
	}
	// Concurrent creates of one name: exactly one wins, the rest see it.
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for range 8 {
		wg.Go(func() {
			res, err := w.Write(ctx, request("race", "x"))
			if err != nil {
				t.Error(err)
				return
			}
			if !res.Existing {
				mu.Lock()
				created++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if created != 1 {
		t.Fatalf("%d creators for one name", created)
	}
	again, err := w.Write(ctx, request("metric", "second"))
	if err != nil || !again.Existing || again.File.Body != "first" {
		t.Fatalf("collision: %+v, %v; want the stored memory", again, err)
	}
	if idx.rows["metric"].Body != "first" {
		t.Fatal("collision changed the index")
	}
	req := request("metric", "second")
	req.Replace = true
	rep, err := w.Write(ctx, req)
	if err != nil || rep.Existing || rep.File.Body != "second" || rep.File.Created != first.File.Created {
		t.Fatalf("replace: %+v, %v", rep, err)
	}
	if _, err := w.Write(ctx, request("Bad Name", "x")); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestArchive(t *testing.T) {
	ctx := t.Context()
	w, r, idx := newWriter(t)
	if _, err := w.Archive(ctx, "missing", "gone"); err == nil {
		t.Fatal("archived a memory that does not exist")
	}
	if _, err := w.Write(ctx, request("old", "x")); err != nil {
		t.Fatal(err)
	}
	f, err := w.Archive(ctx, "old", "superseded")
	if err != nil || f.State != "archived" || idx.rows["old"].State != "archived" {
		t.Fatalf("archive: %+v, %v", f, err)
	}
	if again, err := w.Archive(ctx, "old", "superseded"); err != nil || again.State != "archived" {
		t.Fatalf("archive twice: %+v, %v", again, err)
	}
	if got, err := w.Read(ctx, "old"); err != nil || got.State != "archived" {
		t.Fatalf("file after archive: %+v, %v", got, err)
	}
	if status, _ := r.Status(ctx); status != "" {
		t.Fatalf("uncommitted: %q", status)
	}
}

func TestSearchBoundsItsInput(t *testing.T) {
	w, _, _ := newWriter(t)
	for _, q := range []memory.Query{{Text: " "}, {Text: string(make([]byte, 201))}, {Text: "x", Type: "bogus"}} {
		if _, err := w.Search(t.Context(), q); err == nil {
			t.Errorf("search %+v accepted", q)
		}
	}
}
