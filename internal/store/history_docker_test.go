//go:build docker

package store_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
)

func TestHistoryIndexUpgradeAndSearch(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0016_report_notice_id",
		`INSERT INTO projects (id, name, slug, directory) VALUES ('p', 'Project', 'project', '/project')`,
		`INSERT INTO sessions (id, title, project_id) VALUES ('s', 'old', 'p'), ('other', 'other', NULL)`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
		   ('old', 's', 1, 'assistant', '{ "content": "orchard", "reasoning":"hiddenneedle", "tool_calls": [{"name":"prune", "arguments":{"tree":"apple"}}] }'),
   ('result', 's', 2, 'tool_result', '{"content":"secretneedle"}'),
   ('notice', 's', 3, 'notice', '{"text":"orchard"}'),
   ('summary', 'other', 1, 'compaction', '{"summary":"orchard"}')`,
	)
	old, err := st.Entry(ctx, "old")
	if err != nil || old.ID != "old" || string(old.Payload) != `{ "content": "orchard", "reasoning":"hiddenneedle", "tool_calls": [{"name":"prune", "arguments":{"tree":"apple"}}] }` {
		t.Fatalf("old entry: %+v, %v", old, err)
	}
	added, err := st.AppendEntry(ctx, "s", store.Entry{Kind: store.KindUser, Payload: []byte(`{"content":"orchard orchard"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"prune", "apple"} {
		hits, err := st.SearchHistory(ctx, query, store.HistoryFilter{Limit: 10})
		if err != nil || len(hits) != 1 || hits[0].Entry.ID != "old" {
			t.Fatalf("%s: %+v, %v", query, hits, err)
		}
	}
	for _, query := range []string{"secretneedle", "hiddenneedle"} {
		hits, err := st.SearchHistory(ctx, query, store.HistoryFilter{Limit: 10})
		if err != nil || len(hits) != 0 {
			t.Fatalf("excluded %s: %+v, %v", query, hits, err)
		}
	}
	hits, err := st.SearchHistory(ctx, "orchard", store.HistoryFilter{SessionID: "s", Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].Entry.ID != added.ID {
		t.Fatalf("rank: %+v, %v", hits, err)
	}
	hits, err = st.SearchHistory(ctx, "orchard", store.HistoryFilter{SessionID: "s", Limit: 10, Offset: 1})
	if err != nil || len(hits) != 2 {
		t.Fatalf("page: %+v, %v", hits, err)
	}
	hits, err = st.SearchHistory(ctx, "orchard", store.HistoryFilter{ProjectID: "p", Limit: 10})
	if err != nil || len(hits) != 3 {
		t.Fatalf("project: %+v, %v", hits, err)
	}
	hits, err = st.SearchHistory(ctx, "orchard", store.HistoryFilter{Until: &old.CreatedAt, Limit: 10})
	if err != nil || len(hits) != 0 {
		t.Fatalf("exclusive until: %+v, %v", hits, err)
	}
	future := time.Now().Add(time.Hour)
	hits, err = st.SearchHistory(ctx, "orchard", store.HistoryFilter{Since: &future, Limit: 10})
	if err != nil || len(hits) != 0 {
		t.Fatalf("date: %+v, %v", hits, err)
	}
	window, err := st.HistoryWindow(ctx, "notice", 1, 1)
	if err != nil || len(window) != 3 || window[0].ID != "result" || window[2].ID != added.ID {
		t.Fatalf("window: %+v, %v", window, err)
	}
	window, err = st.HistoryWindow(ctx, "old", 2, 0)
	if err != nil || len(window) != 1 || window[0].ID != "old" {
		t.Fatalf("window boundary: %+v, %v", window, err)
	}
}

func TestHistoryIndexLargeAndNULPayloads(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0016_report_notice_id",
		`INSERT INTO sessions (id, title) VALUES ('s', 'old')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload)
         SELECT 'large', 's', 1, 'user', json_build_object('content', string_agg('word' || n, ' ' ORDER BY n))
         FROM generate_series(1, 180000) n`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
         ('nul', 's', 2, 'assistant', '{"content":"cedar\u0000maple", "tool_calls":[{"name":"find", "arguments":{"value":"near\u0000far"}}]}')`,
	)
	old, err := st.Entry(ctx, "nul")
	if err != nil || string(old.Payload) != `{"content":"cedar\u0000maple", "tool_calls":[{"name":"find", "arguments":{"value":"near\u0000far"}}]}` {
		t.Fatalf("NUL payload: %+v, %v", old, err)
	}
	large, err := st.Entry(ctx, "large")
	var decoded struct {
		Content string `json:"content"`
	}
	if err != nil || large.ID != "large" || json.Unmarshal(large.Payload, &decoded) != nil || !strings.HasSuffix(decoded.Content, "word180000") {
		t.Fatalf("large upgrade: %v", err)
	}
	var text strings.Builder
	for i := 0; i < 180000; i++ {
		fmt.Fprintf(&text, "word%d ", i)
	}
	payload, err := json.Marshal(map[string]any{"content": "newtext", "tool_calls": []any{map[string]any{"name": "largecall", "arguments": map[string]string{"text": text.String()}}}})
	if err != nil {
		t.Fatal(err)
	}
	added, err := st.AppendEntry(ctx, "s", store.Entry{Kind: store.KindAssistant, Payload: payload})
	if err != nil || string(added.Payload) != string(payload) {
		t.Fatalf("large append: %v", err)
	}
	withNUL := []byte(`{"content":"fresh\u0000text"}`)
	added, err = st.AppendEntry(ctx, "s", store.Entry{Kind: store.KindUser, Payload: withNUL})
	if err != nil || string(added.Payload) != string(withNUL) {
		t.Fatalf("NUL append: %v", err)
	}
	for _, query := range []string{"word1", "cedar", "maple", "find", "largecall", "fresh"} {
		hits, err := st.SearchHistory(ctx, query, store.HistoryFilter{Limit: 10})
		if err != nil || len(hits) == 0 {
			t.Fatalf("search %s: %+v, %v", query, hits, err)
		}
		if (query == "cedar" || query == "maple") && (len(hits) != 1 || hits[0].Entry.ID != "nul") {
			t.Fatalf("search NUL entry %s: %+v", query, hits)
		}
	}
}

func TestHistorySearchPaginationTies(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0016_report_notice_id",
		`INSERT INTO sessions (id, title) VALUES ('s', 'old')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload, created_at) VALUES
         ('a', 's', 1, 'user', '{"content":"tieword"}', '2020-01-01'),
         ('b', 's', 2, 'user', '{"content":"tieword"}', '2020-01-01'),
         ('c', 's', 3, 'user', '{"content":"tieword"}', '2020-01-01')`,
	)
	for page, want := range []string{"c", "b", "a"} {
		hits, err := st.SearchHistory(ctx, "tieword", store.HistoryFilter{Limit: 1, Offset: page})
		if err != nil || len(hits) != 1 || hits[0].Entry.ID != want {
			t.Fatalf("page %d: %+v, %v", page, hits, err)
		}
	}
}

func TestHistoryBrowseAndTurns(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0016_report_notice_id",
		`INSERT INTO sessions (id, title) VALUES ('s', 'home')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
   ('u1','s',1,'user','{"content":"one"}'), ('a1','s',2,'assistant','{"content":"reply one"}'),
   ('u2','s',3,'user','{"content":"two"}'), ('a2','s',4,'assistant','{"content":"reply two"}'),
   ('u3','s',5,'user','{"content":"three"}'), ('a3','s',6,'assistant','{"content":"reply three"}'),
   ('u4','s',7,'user','{"content":"four"}'),
   ('call','s',8,'tool_call','{"name":"specialtool","arguments":{"text":"value"}}')`)
	entries, err := st.BrowseHistory(ctx, store.HistoryFilter{SessionID: "s", Limit: 2, Offset: 1}, 6)
	if err != nil || len(entries) != 2 || entries[0].ID != "a2" || entries[1].ID != "u2" {
		t.Fatalf("browse = %+v, %v", entries, err)
	}
	entries, err = st.HistoryTurn(ctx, "a2", 20)
	if err != nil || len(entries) != 6 || entries[0].ID != "u1" || entries[5].ID != "a3" {
		t.Fatalf("turns = %+v, %v", entries, err)
	}
	hits, err := st.SearchHistory(ctx, "specialtool", store.HistoryFilter{SessionID: "s", Limit: 5})
	if err != nil || len(hits) != 1 || hits[0].Entry.ID != "call" {
		t.Fatalf("tool call search = %+v, %v", hits, err)
	}
	entries, err = st.HistoryTurn(ctx, "call", 2)
	if err != nil || len(entries) != 2 || entries[1].ID != "call" || string(entries[1].Payload) != `{"name":"specialtool","arguments":{"text":"value"}}` {
		t.Fatalf("tool call context = %+v, %v", entries, err)
	}
	entries, err = st.BrowseHistory(ctx, store.HistoryFilter{SessionID: "s", Limit: 1}, 9)
	if err != nil || len(entries) != 1 || entries[0].ID != "call" {
		t.Fatalf("tool call browse = %+v, %v", entries, err)
	}
	entry, err := st.EntryBySequence(ctx, "s", 3)
	if err != nil || entry.ID != "u2" {
		t.Fatalf("entry = %+v, %v", entry, err)
	}
}
