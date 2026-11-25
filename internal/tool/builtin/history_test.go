package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

type historyFixture struct {
	builtin.History
	filter    store.HistoryFilter
	before    int64
	entry     store.Entry
	path      []store.Entry
	nearby    []store.Entry
	turnLimit int
}

func (h *historyFixture) Session(_ context.Context, id string) (store.Session, error) {
	if id == "home" {
		return store.Session{ID: id, Kind: store.SessionHome, Name: "home"}, nil
	}
	return store.Session{ID: id, Kind: store.SessionSubagent, Name: "plumber", ProjectID: "p"}, nil
}
func (h *historyFixture) Subagent(_ context.Context, _, _ string) (store.Session, error) {
	return h.Session(context.Background(), "child")
}
func (h *historyFixture) Projects(context.Context) ([]store.Project, error) {
	return []store.Project{{ID: "p", Slug: "house"}}, nil
}
func (h *historyFixture) SearchHistory(_ context.Context, _ string, f store.HistoryFilter) ([]store.HistoryHit, error) {
	h.filter = f
	return []store.HistoryHit{{Entry: h.entry}}, nil
}
func (h *historyFixture) BrowseHistory(_ context.Context, f store.HistoryFilter, before int64) ([]store.Entry, error) {
	h.filter = f
	h.before = before
	return []store.Entry{h.entry}, nil
}
func (h *historyFixture) HistoryTurn(_ context.Context, _ string, limit int) ([]store.Entry, error) {
	h.turnLimit = limit
	if h.nearby != nil {
		return h.nearby[:min(len(h.nearby), limit)], nil
	}
	return []store.Entry{h.entry}, nil
}
func (h *historyFixture) HistoryWindow(_ context.Context, _ string, before, after int) ([]store.Entry, error) {
	if before != 1 || after != 2 {
		panic("wrong window")
	}
	return []store.Entry{h.entry}, nil
}
func (h *historyFixture) Entry(_ context.Context, _ string) (store.Entry, error) { return h.entry, nil }
func (h *historyFixture) EntryBySequence(_ context.Context, _ string, _ int64) (store.Entry, error) {
	return h.entry, nil
}
func (h *historyFixture) SessionPath(context.Context, string) ([]store.Entry, error) {
	return h.path, nil
}

func TestHistoryTools(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	f := &historyFixture{entry: store.Entry{ID: "e", SessionID: "child", Seq: 2, Kind: store.KindUser, CreatedAt: now, Payload: json.RawMessage(`{"content":"` + strings.Repeat("x", 3000) + `"}`)}, path: []store.Entry{{Seq: 1, Kind: store.KindUser}, {Seq: 2, Kind: store.KindUser}, {Seq: 3, Kind: store.KindCompaction, Payload: json.RawMessage(`{"kept":1}`)}}}
	registry, err := builtin.Registry(builtin.Deps{History: f})
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, args string) tool.Result {
		t.Helper()
		tl, ok := registry.Get(name)
		if !ok {
			t.Fatal(name)
		}
		out, err := tl.Call(t.Context(), tool.CallContext{SessionID: "home"}, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	result := call("history_search", `{"query":"plumber","subagent":"plumber","project":"house","since":"2026-11-10","until":"2026-11-10","page":2}`)
	if result.IsError || f.filter.SessionID != "child" || f.filter.ProjectID != "p" || f.filter.Offset != 5 || f.filter.Limit != 5 || !f.filter.Since.Equal(now) || !f.filter.Until.Equal(now.AddDate(0, 0, 1)) || !strings.Contains(result.Content, `"truncated":true`) {
		t.Fatalf("search = %s, filter %+v", result.Content, f.filter)
	}
	result = call("history_search", `{"subagent":"plumber"}`)
	if result.IsError || f.before != 2 || f.filter.SessionID != "child" {
		t.Fatalf("browse = %s, before %d", result.Content, f.before)
	}
	result = call("history_read", `{"around":"#2","subagent":"plumber","limit":4,"full":true}`)
	if result.IsError || !strings.Contains(result.Content, strings.Repeat("x", 3000)) || strings.Contains(result.Content, `"truncated"`) {
		t.Fatalf("full read = %s", result.Content)
	}
	for _, args := range []string{`{}`, `{"entry":"e","around":"e"}`, `{"entry":"e","limit":0}`, `{"entry":"e","full":null}`} {
		if out := call("history_read", args); !out.IsError {
			t.Errorf("accepted %s", args)
		}
	}
	if out := call("history_search", `{"since":"not-a-date"}`); !out.IsError {
		t.Error("accepted invalid date")
	}
}

func TestHistoryAccess(t *testing.T) {
	f := &historyFixture{entry: store.Entry{ID: "result", SessionID: "other", Kind: store.KindToolResult, Payload: json.RawMessage(`{"content":"private result"}`)}}
	registry, err := builtin.Registry(builtin.Deps{History: f})
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, session, args string) tool.Result {
		t.Helper()
		tl, ok := registry.Get(name)
		if !ok {
			t.Fatal(name)
		}
		result, err := tl.Call(t.Context(), tool.CallContext{SessionID: session}, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, name := range []string{"history_search", "history_read"} {
		args := `{}`
		if name == "history_read" {
			args = `{"entry":"result"}`
		}
		if result := call(name, "child", args); !result.IsError {
			t.Errorf("child could call %s", name)
		}
	}
	if result := call("history_read", "home", `{"entry":"result","subagent":"plumber"}`); !result.IsError {
		t.Error("read an entry outside the selected subagent")
	}
	result := call("history_read", "home", `{"entry":"result","full":true}`)
	if result.IsError || !strings.Contains(result.Content, "private result") {
		t.Fatalf("tool result read = %+v", result)
	}
	f.entry.SessionID = "child"
	result = call("history_read", "home", `{"entry":"result","subagent":"plumber","full":true}`)
	if result.IsError || !strings.Contains(result.Content, "private result") {
		t.Fatalf("selected subagent result = %+v", result)
	}
}

func TestHistorySearchBoundsContextAndKeepsHit(t *testing.T) {
	f := &historyFixture{entry: store.Entry{ID: "hit", SessionID: "home", Seq: 5, Kind: store.KindAssistant, Payload: json.RawMessage(`{"content":"hit"}`)}}
	for i := range 200 {
		e := store.Entry{ID: fmt.Sprintf("e%d", i), SessionID: "home", Seq: int64(i + 1), Kind: store.KindAssistant, Payload: json.RawMessage(`{"content":"` + strings.Repeat("x", 3000) + `"}`)}
		if i == 4 {
			e = f.entry
		}
		f.nearby = append(f.nearby, e)
	}
	registry, err := builtin.Registry(builtin.Deps{History: f})
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := registry.Get("history_search")
	result, err := tl.Call(t.Context(), tool.CallContext{SessionID: "home"}, json.RawMessage(`{"query":"hit"}`))
	if err != nil || result.IsError {
		t.Fatalf("search = %+v, %v", result, err)
	}
	var groups []struct {
		Hit       string `json:"hit"`
		Truncated bool   `json:"truncated"`
		Entries   []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(result.Content), &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || !groups[0].Truncated || len(groups[0].Entries) > 8 || len(result.Content) > 8194 || f.turnLimit > 9 {
		t.Fatalf("context not bounded: %d bytes, %+v, limit %d", len(result.Content), groups, f.turnLimit)
	}
	found := false
	for _, e := range groups[0].Entries {
		found = found || e.ID == "hit"
	}
	if !found {
		t.Fatal("bounded context omitted the hit")
	}
}

func TestHistorySearchShowsReadableHitAroundQuery(t *testing.T) {
	text := strings.Repeat("irrelevant ", 250) + "lantern survey access phrase: violet-hedgehog-7319" + strings.Repeat(" filler", 400)
	payload, err := json.Marshal(map[string]any{"role": "user", "content": text, "reasoning": "private reasoning", "metrics": map[string]any{"input_tokens": 999}})
	if err != nil {
		t.Fatal(err)
	}
	f := &historyFixture{entry: store.Entry{ID: "hit", SessionID: "home", Seq: 2, Kind: store.KindUser, Payload: payload}}
	f.nearby = []store.Entry{{ID: "assistant", SessionID: "home", Seq: 1, Kind: store.KindAssistant, Payload: json.RawMessage(`{"role":"assistant","content":"A useful answer","reasoning":"hidden reasoning","metrics":{"input_tokens":999}}`)}, f.entry}
	registry, err := builtin.Registry(builtin.Deps{History: f})
	if err != nil {
		t.Fatal(err)
	}
	search, _ := registry.Get("history_search")
	result, err := search.Call(t.Context(), tool.CallContext{SessionID: "home"}, json.RawMessage(`{"query":"lantern survey"}`))
	if err != nil || result.IsError {
		t.Fatalf("search: %+v %v", result, err)
	}
	var groups []struct {
		Entries []struct {
			Content   string `json:"content"`
			Truncated bool   `json:"truncated"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(result.Content), &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Entries) != 2 {
		t.Fatalf("groups: %+v", groups)
	}
	if groups[0].Entries[0].Content != "A useful answer" {
		t.Fatalf("assistant content = %q", groups[0].Entries[0].Content)
	}
	got := groups[0].Entries[1]
	if !got.Truncated || !strings.Contains(got.Content, "lantern survey access phrase: violet-hedgehog-7319") || strings.Contains(got.Content, "private reasoning") || strings.Contains(got.Content, "input_tokens") {
		t.Fatalf("excerpt: %+v", got)
	}
}
