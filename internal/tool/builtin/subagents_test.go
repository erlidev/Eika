package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// subagentFake records what the tools asked for, by calling session.
type subagentFake struct {
	rows    map[string]store.Session
	running map[string]bool
	calls   []string
}

func (f *subagentFake) StartSubagent(_ context.Context, home string, child store.Session) (store.Session, bool, error) {
	f.calls = append(f.calls, "start "+home+" "+child.Name+" "+child.ProjectID)
	if old, ok := f.rows[child.Name]; ok {
		return old, false, nil
	}
	child.State = store.SessionRunning
	f.rows[child.Name] = child
	return child, true, nil
}
func (f *subagentFake) MessageSubagent(_ context.Context, home, name, text, questionID string) (store.Session, bool, error) {
	f.calls = append(f.calls, "message "+home+" "+name+" "+text)
	row, ok := f.rows[name]
	if !ok {
		return store.Session{}, false, store.ErrNotFound
	}
	if questionID == "question-old" {
		return row, false, nil
	}
	if f.running[name] {
		return row, false, nil
	}
	row.State = store.SessionRunning
	return row, true, nil
}
func (f *subagentFake) Subagents(_ context.Context, home, name string) ([]store.Session, error) {
	f.calls = append(f.calls, "status "+home+" "+name)
	if name != "" {
		row, ok := f.rows[name]
		if !ok {
			return nil, store.ErrNotFound
		}
		return []store.Session{row}, nil
	}
	return []store.Session{f.rows["plumber"], f.rows["flights"]}, nil
}
func (f *subagentFake) CancelSubagent(_ context.Context, home, name string) (store.Session, error) {
	f.calls = append(f.calls, "cancel "+home+" "+name)
	row := f.rows[name]
	row.State = store.SessionCancelled
	return row, nil
}

func TestSubagentTools(t *testing.T) {
	f := &subagentFake{
		rows: map[string]store.Session{"flights": {Name: "flights", Agent: "researcher", State: store.SessionWaiting, WaitingReason: "a question",
			Brief: "Find flights", Result: "Two options\nfull detail", LastActiveAt: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}},
		running: map[string]bool{"flights": true},
	}
	projects := &projectFake{rows: []store.Project{{ID: "p1", Slug: "kitchen", ProjectFields: store.ProjectFields{Name: "Kitchen", Status: "active"}}}}
	call := func(name, args string) tool.Result {
		t.Helper()
		result, err := (subagentTool{name: name, subagents: f, projects: projects}).Call(context.Background(), tool.CallContext{SessionID: "home"}, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	view := func(r tool.Result) map[string]any {
		t.Helper()
		var out map[string]any
		if r.IsError || json.Unmarshal([]byte(r.Content), &out) != nil {
			t.Fatalf("result = %+v", r)
		}
		return out
	}

	started := view(call("subagent_start", `{"agent":"worker","name":"plumber","brief":"Find a plumber","project":"kitchen"}`))
	if started["state"] != "running" || started["project"] != "kitchen" || !strings.HasPrefix(started["note"].(string), "Started") {
		t.Fatalf("start = %v", started)
	}
	again := view(call("subagent_start", `{"agent":"researcher","name":"plumber","brief":"Other"}`))
	if again["agent"] != "worker" || !strings.Contains(again["note"].(string), "already exists") {
		t.Fatalf("duplicate start = %v", again)
	}
	running := view(call("subagent_message", `{"subagent":"flights","text":"cheaper"}`))
	if running["state"] != "waiting" || !strings.Contains(running["note"].(string), "Include its question_id") {
		t.Fatalf("running message = %v", running)
	}
	stale := view(call("subagent_message", `{"subagent":"flights","text":"old answer","question_id":"question-old"}`))
	if stale["note"] != "That question is no longer pending. The message was not accepted." {
		t.Fatalf("stale answer = %v", stale)
	}
	resumed := view(call("subagent_message", `{"subagent":"plumber","text":"get a second quote"}`))
	if !strings.HasPrefix(resumed["note"].(string), "Accepted") {
		t.Fatalf("resume = %v", resumed)
	}
	if r := call("subagent_message", `{"subagent":"nobody","text":"hi"}`); !r.IsError || !strings.Contains(r.Content, `no subagent is named "nobody"`) {
		t.Fatalf("unknown subagent = %+v", r)
	}

	var board []map[string]any
	if r := call("subagent_status", `{}`); json.Unmarshal([]byte(r.Content), &board) != nil || len(board) != 2 {
		t.Fatalf("board = %+v", r)
	}
	if board[1]["report"] != "Two options" || board[1]["waiting_on"] != "a question" || board[1]["last_active_at"] != "2026-11-15T12:00:00Z" || board[1]["brief"] != nil {
		t.Fatalf("board row = %v", board[1])
	}
	detail := view(call("subagent_status", `{"subagent":"flights"}`))
	if detail["brief"] != "Find flights" || detail["report"] != "Two options\nfull detail" {
		t.Fatalf("detail = %v", detail)
	}
	if cancelled := view(call("subagent_cancel", `{"subagent":"plumber"}`)); cancelled["state"] != "cancelled" {
		t.Fatalf("cancel = %v", cancelled)
	}

	for _, tc := range []struct{ name, args, want string }{
		{"subagent_start", `{"agent":"eika","name":"x","brief":"b"}`, "agent must be"},
		{"subagent_start", `{"agent":"worker","name":"Has Space","brief":"b"}`, "lowercase"},
		{"subagent_start", `{"agent":"worker","name":"x","brief":"  "}`, "brief must be nonempty"},
		{"subagent_start", `{"agent":"worker","name":"x","brief":"b","project":"missing"}`, `project "missing" not found`},
		{"subagent_start", `{"agent":"worker","name":"x","brief":"b","extra":"y"}`, "unknown argument"},
		{"subagent_start", `{"agent":"worker","name":"x"}`, "brief is required"},
		{"subagent_message", `{"subagent":"plumber","text":"\ud800"}`, "surrogate"},
		{"subagent_message", `{"subagent":"plumber","text":"hello","question_id":""}`, "question_id"},
		{"subagent_message", "{\"subagent\":\"plumber\",\"text\":\"\xff\"}", "UTF-8"},
		{"subagent_status", `{"subagent":null}`, "must be a string"},
		{"subagent_cancel", `{}`, "subagent is required"},
	} {
		if r := call(tc.name, tc.args); !r.IsError || !strings.Contains(r.Content, tc.want) {
			t.Errorf("%s %s = %+v, want error with %q", tc.name, tc.args, r, tc.want)
		}
	}
	want := []string{"start home plumber p1", "start home plumber ", "message home flights cheaper", "message home flights old answer", "message home plumber get a second quote",
		"message home nobody hi", "status home ", "status home flights", "cancel home plumber"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", f.calls, want)
	}
}

func TestSubagentToolsWithoutABindingAnswerTheModel(t *testing.T) {
	r, err := (subagentTool{name: "subagent_status"}).Call(context.Background(), tool.CallContext{SessionID: "home"}, json.RawMessage(`{}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "unavailable") {
		t.Fatalf("result = %+v, %v", r, err)
	}
}
