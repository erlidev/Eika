package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/tool"
)

type memFake struct {
	writes   []memory.Request
	archived [][2]string
	existing *memory.File
	hits     []memory.Hit
	file     memory.File
	query    memory.Query
}

func (f *memFake) Search(_ context.Context, q memory.Query) ([]memory.Hit, error) {
	f.query = q
	return f.hits, nil
}
func (f *memFake) Read(_ context.Context, name string) (memory.File, error) {
	if f.file.Name != name {
		return memory.File{}, memory.ErrNotFound
	}
	return f.file, nil
}
func (f *memFake) Write(_ context.Context, r memory.Request) (memory.Result, error) {
	if f.existing != nil && !r.Replace {
		return memory.Result{File: *f.existing, Existing: true}, nil
	}
	f.writes = append(f.writes, r)
	return memory.Result{File: memory.File{Name: r.Name, Description: r.Description, Type: r.Type, State: r.State, Body: r.Body}}, nil
}
func (f *memFake) Archive(_ context.Context, name, reason string) (memory.File, error) {
	if name != "known" {
		return memory.File{}, memory.ErrNotFound
	}
	f.archived = append(f.archived, [2]string{name, reason})
	return memory.File{Name: name, State: "archived"}, nil
}

type checkFake struct {
	v   sentry.MemoryVerdict
	err error
	got []sentry.MemoryCandidate
}

func (c *checkFake) CheckMemory(_ context.Context, cand sentry.MemoryCandidate) (sentry.MemoryVerdict, error) {
	c.got = append(c.got, cand)
	return c.v, c.err
}

const goodWrite = `{"name":"prefers-metric","description":"Use when giving measurements or recipes.","type":"preference","body":"Metric units."}`

func memCall(t *testing.T, name string, m Memories, c sentry.MemoryChecker, args string) tool.Result {
	t.Helper()
	r, err := memoryTool{name: name, memory: m, check: c}.Call(context.Background(),
		tool.CallContext{SessionID: "s1", RunID: "r1", CallID: "c1"}, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

func TestMemoryWriteFollowsTheCheck(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verdict   sentry.MemoryVerdict
		err       error
		wantWrite string // state written, "" for none
		wantError string
	}{
		{name: "active", verdict: sentry.MemoryVerdict{Decision: sentry.MemoryActive}, wantWrite: "active"},
		{name: "pending", verdict: sentry.MemoryVerdict{Decision: sentry.MemoryPending, MaxScore: 0.9,
			Spans: []sentry.MemorySpan{{Field: "body", Start: 1, End: 4, Score: 0.9, Text: "etr"}}}, wantWrite: "pending"},
		{name: "unscanned", verdict: sentry.MemoryVerdict{Decision: sentry.MemoryPending, Unscanned: true}, wantWrite: "pending"},
		{name: "deny", verdict: sentry.MemoryVerdict{Decision: sentry.MemoryDeny, Reason: "this memory contains a credential (aws key) in its body."}, wantError: "contains a credential"},
		{name: "reject", verdict: sentry.MemoryVerdict{Decision: sentry.MemoryReject, Reason: "screening is required and unavailable."}, wantError: "screening is required"},
		{name: "check failed", err: errors.New("db down"), wantError: "cannot be saved right now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c := &memFake{}, &checkFake{v: tc.verdict, err: tc.err}
			r := memCall(t, "memory_write", m, c, goodWrite)
			if tc.wantWrite == "" {
				if len(m.writes) != 0 {
					t.Fatalf("wrote %+v, want nothing", m.writes)
				}
				if !r.IsError || !strings.Contains(r.Content, tc.wantError) {
					t.Fatalf("result = %+v, want error with %q", r, tc.wantError)
				}
				return
			}
			if r.IsError || len(m.writes) != 1 || m.writes[0].State != tc.wantWrite {
				t.Fatalf("result = %+v, writes = %+v, want one %s write", r, m.writes, tc.wantWrite)
			}
			if got := m.writes[0].Screen; (tc.wantWrite == "pending") != (got != nil) ||
				got != nil && (got.MaxScore != tc.verdict.MaxScore || got.Unscanned != tc.verdict.Unscanned || len(got.Spans) != len(tc.verdict.Spans)) {
				t.Fatalf("screen = %+v, verdict %+v", got, tc.verdict)
			}
			if len(c.got) != 1 || c.got[0].Body != "Metric units." || c.got[0].Principal.Session != "s1" || c.got[0].Key != "s1:c1" {
				t.Fatalf("candidate = %+v", c.got)
			}
		})
	}
}

func TestMemoryWriteRefusesWithoutCheckerOrWithBadFormat(t *testing.T) {
	m := &memFake{}
	if r := memCall(t, "memory_write", m, nil, goodWrite); !r.IsError || len(m.writes) != 0 {
		t.Fatalf("no checker: %+v, writes %v", r, m.writes)
	}
	c := &checkFake{v: sentry.MemoryVerdict{Decision: sentry.MemoryActive}}
	for _, args := range []string{
		`{"name":"Bad Name","description":"Use when giving measurements or recipes.","type":"preference","body":"x"}`,
		`{"name":"ok","description":"short","type":"preference","body":"x"}`,
		`{"name":"ok","description":"Use when giving measurements or recipes.","type":"preference"}`,
		`{"name":"ok","description":"Use when giving measurements or recipes.","type":"preference","body":"x","pinned":true}`,
	} {
		if r := memCall(t, "memory_write", m, c, args); !r.IsError {
			t.Errorf("%s: want error", args)
		}
	}
	if len(c.got) != 0 || len(m.writes) != 0 {
		t.Fatalf("invalid input reached the check or writer: %v %v", c.got, m.writes)
	}
}

func TestMemoryWriteNameCollisionReturnsExisting(t *testing.T) {
	m := &memFake{existing: &memory.File{Name: "prefers-metric", Description: "Use when giving measurements or recipes.", Type: "preference", State: "active", Body: "Old text."}}
	c := &checkFake{v: sentry.MemoryVerdict{Decision: sentry.MemoryActive}}
	r := memCall(t, "memory_write", m, c, goodWrite)
	if r.IsError || !strings.Contains(r.Content, `"saved":false`) || !strings.Contains(r.Content, "Old text.") || len(m.writes) != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestMemorySearchAndReadMarkPending(t *testing.T) {
	m := &memFake{
		hits: []memory.Hit{{Name: "a", Description: "d", Type: "fact", State: "pending", Snippet: "s"}, {Name: "b", Type: "fact", State: "active"}},
		file: memory.File{Name: "a", Type: "fact", State: "pending", Body: "text"},
	}
	r := memCall(t, "memory_search", m, nil, `{"query":" metric ","limit":3}`)
	var hits []memoryView
	if err := json.Unmarshal([]byte(r.Content), &hits); err != nil || len(hits) != 2 || !hits[0].Pending || hits[0].Note == "" || hits[1].Pending {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	if m.query.Text != "metric" || m.query.Limit != 3 || m.query.IncludeArchived {
		t.Fatalf("query = %+v", m.query)
	}
	var got memoryView
	if r = memCall(t, "memory_read", m, nil, `{"name":"a"}`); json.Unmarshal([]byte(r.Content), &got) != nil || !got.Pending || got.Body != "text" {
		t.Fatalf("read = %+v", r)
	}
	for _, args := range []string{`{"query":""}`, `{"query":"x","type":"nope"}`, `{"query":"x","limit":999}`, `{"query":"` + strings.Repeat("x", 201) + `"}`} {
		if r := memCall(t, "memory_search", m, nil, args); !r.IsError {
			t.Errorf("%.40s: want error", args)
		}
	}
	if r := memCall(t, "memory_read", m, nil, `{"name":"zzz"}`); !r.IsError {
		t.Error("missing memory: want error")
	}
}

func TestMemoryForgetArchivesWithReason(t *testing.T) {
	m := &memFake{}
	if r := memCall(t, "memory_forget", m, nil, `{"name":"known","reason":"user asked"}`); r.IsError || len(m.archived) != 1 || m.archived[0] != [2]string{"known", "user asked"} {
		t.Fatalf("forget = %+v, %v", r, m.archived)
	}
	for _, args := range []string{`{"name":"known","reason":"  "}`, `{"name":"known"}`, `{"name":"nope","reason":"x"}`} {
		if r := memCall(t, "memory_forget", m, nil, args); !r.IsError {
			t.Errorf("%s: want error", args)
		}
	}
	if len(m.archived) != 1 {
		t.Fatalf("archived = %v", m.archived)
	}
}

// Only looking is read-only, and the tools are for an agent definition alone.
func TestMemoryToolsAreMarked(t *testing.T) {
	for name, readOnly := range map[string]bool{"memory_search": true, "memory_read": true, "memory_write": false, "memory_forget": false} {
		tl := memoryTool{name: name}
		if tool.SideEffecting(tl) == readOnly {
			t.Errorf("%s read-only = %v, want %v", name, !tool.SideEffecting(tl), readOnly)
		}
		if _, ok := any(tl).(tool.AgentOnly); !ok || tool.NeedsComputer(tl) {
			t.Errorf("%s must be agent-only and need no computer", name)
		}
	}
}
