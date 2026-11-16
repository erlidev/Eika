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
		{`{"summary":"s","outputs":"a.md"}`, "outputs must be a list"},
		{`{"summary":"s","outputs":[` + items + `]}`, "at most 20 entries"},
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
}
