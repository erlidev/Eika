package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var secNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// words counts whitespace-separated fields: deterministic and monotonic.
func words(_ context.Context, s string) (int, bool, error) { return len(strings.Fields(s)), true, nil }

func mem(name, typ, updated string, pinned bool, body string) File {
	return File{Name: name, Type: typ, Description: "Use when " + name + " matters", Updated: updated,
		Pinned: pinned, State: "active", Body: body}
}

func TestBuildSectionOrderAndExclusions(t *testing.T) {
	exp := mem("old", "fact", "2026-11-01T00:00:00Z", true, "x")
	exp.Expires = "2026-11-21"
	pend := mem("pend", "fact", "2026-11-01T00:00:00Z", true, "x")
	pend.State = "pending"
	arch := mem("arch", "fact", "2026-11-01T00:00:00Z", false, "x")
	arch.State = "archived"
	live := mem("live", "fact", "2026-11-01T00:00:00Z", false, "x")
	live.Expires = "2026-11-22" // expires today: still loads
	in := []File{exp, pend, arch, live,
		mem("zeta", "user", "2026-11-02T00:00:00Z", true, "Zeta body"),
		mem("alpha", "preference", "2026-11-02T00:00:00Z", true, "Alpha body"),
		mem("beta", "fact", "2026-11-03T00:00:00Z", false, "ignored")}
	s, err := BuildSection(context.Background(), words, in, secNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "## Memory\nPinned:\n[alpha] (preference)\nAlpha body\n[zeta] (user)\nZeta body\n" +
		"Index (memory_read <name> for details, memory_search for anything not listed):\n" +
		"- beta: Use when beta matters\n- live: Use when live matters\n"
	if s.Text != want || s.Overflow != 0 || s.Pinned != 2 || s.Indexed != 2 {
		t.Fatalf("got %+v\n%s", s, s.Text)
	}
	again, _ := BuildSection(context.Background(), words, in, secNow)
	if again.Text != s.Text {
		t.Fatal("not deterministic")
	}
}

func TestBuildSectionBudgetEdgeAndOverflow(t *testing.T) {
	var in []File
	for i := 0; i < 10; i++ {
		in = append(in, mem(fmt.Sprintf("m%02d", i), "fact", fmt.Sprintf("2026-11-%02dT00:00:00Z", i+1), false, ""))
	}
	// Budget in words is fixed at 3000; use a counter that charges 600 per line
	// so exactly 4 index entries fit with the headers and overflow line.
	heavy := func(_ context.Context, s string) (int, bool, error) {
		return strings.Count(s, "\n- ")*600 + strings.Count(s, "more memories")*5, true, nil
	}
	s, err := BuildSection(context.Background(), heavy, in, secNow)
	if err != nil {
		t.Fatal(err)
	}
	if s.Indexed != 4 || s.Overflow != 6 || s.Tokens != 2405 {
		t.Fatalf("%+v", s)
	}
	// Newest four, rendered by name.
	if !strings.Contains(s.Text, "- m09:") || !strings.Contains(s.Text, "- m06:") || strings.Contains(s.Text, "- m05:") ||
		!strings.Contains(s.Text, "(6 more memories; search to find them)") {
		t.Fatal(s.Text)
	}
}

func TestBuildSectionPinsFirstAndCapped(t *testing.T) {
	var in []File
	for i := 0; i < 14; i++ {
		in = append(in, mem(fmt.Sprintf("p%02d", i), "fact", fmt.Sprintf("2026-11-%02dT00:00:00Z", i+1), true, "b"))
	}
	s, err := BuildSection(context.Background(), words, in, secNow)
	if err != nil {
		t.Fatal(err)
	}
	if s.Pinned != 12 || s.Indexed != 2 || s.Overflow != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestBuildSectionCountFailure(t *testing.T) {
	boom := errors.New("down")
	_, err := BuildSection(context.Background(), func(context.Context, string) (int, bool, error) { return 0, false, boom }, nil, secNow)
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	_, err = BuildSection(context.Background(), func(context.Context, string) (int, bool, error) { return 1, false, nil }, nil, secNow)
	if !errors.Is(err, ErrNotExact) {
		t.Fatal(err)
	}
}

func TestCheckPinBudget(t *testing.T) {
	ctx := context.Background()
	var pins []File
	for i := 0; i < 12; i++ {
		pins = append(pins, mem(fmt.Sprintf("p%02d", i), "fact", "2026-11-01T00:00:00Z", true, "b"))
	}
	if err := CheckPinBudget(ctx, words, pins, pins[0], secNow); err != nil {
		t.Fatalf("editing an existing pin: %v", err)
	}
	extra := mem("extra", "fact", "2026-11-01T00:00:00Z", true, "b")
	if err := CheckPinBudget(ctx, words, pins, extra, secNow); !errors.Is(err, ErrTooManyPins) {
		t.Fatal(err)
	}
	extra.Pinned = false
	if err := CheckPinBudget(ctx, words, pins, extra, secNow); err != nil {
		t.Fatal(err)
	}
	big := mem("big", "fact", "2026-11-01T00:00:00Z", true, strings.Repeat("w ", 3000))
	if err := CheckPinBudget(ctx, words, nil, big, secNow); !errors.Is(err, ErrPinsOverBudget) {
		t.Fatal(err)
	}
	boom := errors.New("down")
	if err := CheckPinBudget(ctx, func(context.Context, string) (int, bool, error) { return 0, false, boom }, nil, big, secNow); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}
