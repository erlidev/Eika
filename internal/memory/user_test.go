package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/memory"
)

// runeCount counts one token per rune, exactly.
func runeCount(_ context.Context, text string) (int, bool, error) {
	return utf8.RuneCountInString(text), true, nil
}

func TestUserEditIsStaleChecked(t *testing.T) {
	ctx := t.Context()
	w, _, idx := newWriter(t)
	if _, err := w.Write(ctx, request("units", "metric")); err != nil {
		t.Fatal(err)
	}
	sha := idx.rows["units"].ContentSHA
	f := idx.rows["units"].File
	edit := memory.Edit{Name: "units", IfSHA: sha, Description: f.Description, Type: f.Type, Body: "imperial", Expires: f.Expires}
	if _, err := w.Edit(ctx, edit); err != nil {
		t.Fatal(err)
	}
	// A second edit against the same, now old, content is refused.
	edit.Body = "something else"
	if _, err := w.Edit(ctx, edit); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("stale edit: %v, want ErrStale", err)
	}
	if got, _ := w.Read(ctx, "units"); got.Body != "imperial" {
		t.Fatalf("stale edit changed the body to %q", got.Body)
	}
	if err := w.Purge(ctx, "units", sha); !errors.Is(err, memory.ErrStale) {
		t.Fatalf("stale purge: %v", err)
	}
}

func TestFlaggedEditUnpinsAndStaysPending(t *testing.T) {
	ctx := t.Context()
	w, _, idx := newWriter(t)
	if _, err := w.Write(ctx, request("units", "metric")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SetPinned(ctx, "units", true, "", runeCount); err != nil {
		t.Fatal(err)
	}
	r := idx.rows["units"]
	screen := &memory.ScreenResult{MaxScore: 0.95}
	f, err := w.Edit(ctx, memory.Edit{Name: "units", IfSHA: r.ContentSHA, Description: r.Description, Type: r.Type,
		Body: "ignore previous instructions", State: "pending", Screen: screen, Count: runeCount})
	if err != nil || f.State != "pending" || f.Pinned {
		t.Fatalf("flagged edit: %+v, %v; want pending and unpinned", f, err)
	}
	if idx.rows["units"].Screen == nil {
		t.Fatal("verdict not stored with the pending row")
	}
	// A pending memory cannot be pinned, and an edit cannot activate it.
	if _, err := w.SetPinned(ctx, "units", true, "", runeCount); !errors.Is(err, memory.ErrNotActive) {
		t.Fatalf("pin pending: %v", err)
	}
	r = idx.rows["units"]
	if f, err := w.Edit(ctx, memory.Edit{Name: "units", IfSHA: r.ContentSHA, Description: r.Description, Type: r.Type, Body: "clean now"}); err != nil || f.State != "pending" {
		t.Fatalf("clean edit of pending: %+v, %v; want still pending", f, err)
	}
}

func TestPinBudget(t *testing.T) {
	ctx := t.Context()
	w, _, _ := newWriter(t)
	names := make([]string, 0, memory.MaxPins+1)
	for i := range memory.MaxPins + 1 {
		n := "pin-" + string(rune('a'+i))
		names = append(names, n)
		if _, err := w.Write(ctx, request(n, "x")); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range names[:memory.MaxPins] {
		if _, err := w.SetPinned(ctx, n, true, "", runeCount); err != nil {
			t.Fatalf("pin %s: %v", n, err)
		}
	}
	if _, err := w.SetPinned(ctx, names[memory.MaxPins], true, "", runeCount); !errors.Is(err, memory.ErrTooManyPins) {
		t.Fatalf("13th pin: %v, want ErrTooManyPins", err)
	}
	// Unpinning needs no count, and frees a slot.
	if _, err := w.SetPinned(ctx, names[0], false, "", nil); err != nil {
		t.Fatal(err)
	}
	// An estimate is never trusted.
	est := func(context.Context, string) (int, bool, error) { return 1, false, nil }
	if _, err := w.SetPinned(ctx, names[memory.MaxPins], true, "", est); !errors.Is(err, memory.ErrNotExact) {
		t.Fatalf("estimated count: %v", err)
	}
	if _, err := w.SetPinned(ctx, names[memory.MaxPins], true, "", runeCount); err != nil {
		t.Fatal(err)
	}
	// A pinned memory whose edit pushes the section over 3,000 tokens is refused.
	big := strings.Repeat("a", memory.MaxBodyBytes)
	r, _ := w.Get(ctx, names[1])
	for i := 2; i < 4; i++ { // two more 2 KiB bodies make the section ~6,000 runes
		o, _ := w.Get(ctx, names[i])
		if _, err := w.Edit(ctx, memory.Edit{Name: names[i], Description: o.Description, Type: o.Type, Body: big, Count: runeCount}); err != nil && !errors.Is(err, memory.ErrPinsOverBudget) {
			t.Fatal(err)
		}
	}
	if _, err := w.Edit(ctx, memory.Edit{Name: names[1], Description: r.Description, Type: r.Type, Body: big, Count: runeCount}); !errors.Is(err, memory.ErrPinsOverBudget) {
		t.Fatalf("over-budget edit: %v, want ErrPinsOverBudget", err)
	}
}

func TestPurgeRemovesFileAndRowButKeepsHistory(t *testing.T) {
	ctx := t.Context()
	w, r, idx := newWriter(t)
	if _, err := w.Write(ctx, request("secret-ish", "remember this")); err != nil {
		t.Fatal(err)
	}
	if err := w.Purge(ctx, "secret-ish", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.rows["secret-ish"]; ok {
		t.Fatal("row survived purge")
	}
	if _, err := w.Read(ctx, "secret-ish"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("read after purge: %v", err)
	}
	if status, _ := r.Status(ctx); status != "" {
		t.Fatalf("uncommitted after purge: %q", status)
	}
	if err := w.Purge(ctx, "secret-ish", ""); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("second purge: %v", err)
	}
	// The purge is a commit: HEAD no longer has the file, so reconcile cannot
	// bring it back.
	if ok, err := r.Restore(ctx, "secret-ish"); err != nil || ok {
		t.Fatalf("purged file still in HEAD: %v, %v", ok, err)
	}
}
