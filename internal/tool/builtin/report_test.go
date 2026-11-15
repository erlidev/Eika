package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/tool"
)

type reportFake struct {
	err   error
	calls []string
	lists [][]string
}

func (f *reportFake) Report(_ context.Context, session, summary string, outputs, followUps []string) error {
	f.calls = append(f.calls, session+"|"+summary)
	f.lists = append(f.lists, outputs, followUps)
	return f.err
}

func TestReportTool(t *testing.T) {
	f := &reportFake{}
	call := func(args string) tool.Result {
		t.Helper()
		r, err := (reportTool{reports: f}).Call(context.Background(), tool.CallContext{SessionID: "child"}, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := call(`{"summary":"Done\nwith detail","outputs":["a.md","https://example.com"],"follow_ups":["call them"]}`); r.IsError {
		t.Fatalf("report = %+v", r)
	}
	if r := call(`{"summary":"Only a summary"}`); r.IsError {
		t.Fatalf("summary only = %+v", r)
	}
	if want := []string{"child|Done\nwith detail", "child|Only a summary"}; !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %q", f.calls)
	}
	if want := [][]string{{"a.md", "https://example.com"}, {"call them"}, nil, nil}; !reflect.DeepEqual(f.lists, want) {
		t.Fatalf("lists = %q", f.lists)
	}
	calls := len(f.calls)
	items := `"x"` + strings.Repeat(`,"x"`, 20)
	for _, tc := range []struct{ args, want string }{
		{`{}`, "summary is required"},
		{`{"summary":"  "}`, "summary must be nonempty"},
		{`{"summary":"a\u0000b"}`, "summary must be nonempty"},
		{`{"summary":"s","extra":1}`, "unknown argument"},
		{`{"summary":"s","summary":"t"}`, "duplicate argument"},
		{`{"summary":1}`, "must be a string"},
		{`{"summary":"\ud800"}`, "surrogate"},
		{"{\"summary\":\"\xff\"}", "UTF-8"},
		{`{"summary":"s","outputs":"a.md"}`, "outputs must be a list"},
		{`{"summary":"s","outputs":null}`, "outputs must be a list"},
		{`{"summary":"s","outputs":[1]}`, "must be a string"},
		{`{"summary":"s","outputs":["\udc00"]}`, "surrogate"},
		{`{"summary":"s","follow_ups":[""]}`, "each of follow_ups"},
		{`{"summary":"s","follow_ups":["` + strings.Repeat("a", 1025) + `"]}`, "each of follow_ups"},
		{`{"summary":"s","outputs":[` + items + `]}`, "at most 20 entries"},
		{`{"summary":"` + strings.Repeat("a", 32<<10+1) + `"}`, "summary must be nonempty"},
		{`[]`, "must be an object"},
	} {
		if r := call(tc.args); !r.IsError || !strings.Contains(r.Content, tc.want) {
			t.Errorf("%.80s = %+v, want error with %q", tc.args, r, tc.want)
		}
	}
	if len(f.calls) != calls {
		t.Errorf("invalid reports reached the service: %q", f.calls[calls:])
	}

	f.err = errors.New("subagent has no active assignment to report on")
	if r := call(`{"summary":"late"}`); !r.IsError || !strings.Contains(r.Content, "no active assignment") {
		t.Errorf("refused report = %+v", r)
	}
	if r, err := (reportTool{}).Call(context.Background(), tool.CallContext{}, json.RawMessage(`{"summary":"s"}`)); err != nil || !r.IsError || !strings.Contains(r.Content, "unavailable") {
		t.Errorf("unbound report = %+v, %v", r, err)
	}
}
