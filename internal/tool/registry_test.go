package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/tool"
)

// stub is a tool that returns its own name, for registry tests.
type stub struct{ name string }

func (s stub) Name() string        { return s.name }
func (s stub) Description() string { return "stub " + s.name }
func (s stub) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s stub) Call(context.Context, tool.CallContext, json.RawMessage) (tool.Result, error) {
	return tool.Text(s.name), nil
}

func TestRegistry(t *testing.T) {
	r, err := tool.NewRegistry(stub{name: "b"}, stub{name: "a"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := r.Get("a"); !ok {
		t.Error("Get(a) did not find the tool")
	}
	if _, ok := r.Get("missing"); ok {
		t.Error("Get(missing) found a tool")
	}
	list := r.List()
	if len(list) != 2 || list[0].Name() != "a" || list[1].Name() != "b" {
		t.Errorf("List is not sorted by name: %v", list)
	}
	schemas := r.Schemas()
	if len(schemas) != 2 || schemas[0].Name != "a" || schemas[0].Description != "stub a" {
		t.Errorf("Schemas = %+v", schemas)
	}
}

func TestRegistryRejectsBadRegistration(t *testing.T) {
	cases := []struct {
		name string
		tool tool.Tool
	}{
		{"nil tool", nil},
		{"empty name", stub{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := tool.NewRegistry(c.tool); err == nil {
				t.Error("NewRegistry returned no error")
			}
		})
	}
	if _, err := tool.NewRegistry(stub{name: "a"}, stub{name: "a"}); err == nil {
		t.Error("NewRegistry accepted a duplicate name")
	}
}

func TestCallContextOutput(t *testing.T) {
	var got []event.Event
	c := tool.CallContext{
		Emit:      event.EmitterFunc(func(_ context.Context, e event.Event) { got = append(got, e) }),
		SessionID: "s1",
		RunID:     "r1",
		CallID:    "c1",
	}
	ctx := context.Background()
	c.Output(ctx, "hello")
	c.Output(ctx, "")

	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1", len(got))
	}
	if got[0].Type != event.TypeToolOutput || got[0].Topic != event.SessionTopic("s1") {
		t.Errorf("event = %+v", got[0])
	}
	var payload event.ToolOutput
	if err := got[0].DecodePayload(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload != (event.ToolOutput{RunID: "r1", CallID: "c1", Text: "hello"}) {
		t.Errorf("payload = %+v", payload)
	}

	// A call context without an emitter drops output instead of panicking.
	tool.CallContext{}.Output(ctx, "hello")
}
