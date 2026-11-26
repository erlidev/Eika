package builtin

import (
	"context"
	"encoding/json"

	"github.com/erlidev/eika/internal/tool"
)

// ScheduleTools handles schedule tool calls for the calling session.
type ScheduleTools func(context.Context, string, string, json.RawMessage) (string, error)
type scheduleTool struct {
	name string
	call ScheduleTools
}

func (t scheduleTool) Name() string   { return t.name }
func (t scheduleTool) Standalone()    {}
func (t scheduleTool) AgentOnly()     {}
func (t scheduleTool) ReadOnly() bool { return t.name == "routine_list" }
func (t scheduleTool) Description() string {
	switch t.name {
	case "routine_create":
		return "Create a disabled routine for user approval. Provide a cron or RFC 3339 schedule."
	case "routine_list":
		return "List routines and their schedules."
	case "routine_update":
		return "Update a routine you created. The user must approve it before it can run."
	case "routine_delete":
		return "Delete a routine you created."
	default:
		return "Resume this continuing session later. Supply either an RFC 3339 time in at or a duration such as 1h in in, and a note."
	}
}
func (t scheduleTool) Schema() json.RawMessage {
	props := map[string]any{}
	required := []string{}
	fields := []string{}
	switch t.name {
	case "routine_create":
		fields = []string{"name", "description", "schedule_kind", "schedule_expr", "timezone", "agent", "project_id", "prompt"}
		required = []string{"name", "schedule_kind", "schedule_expr", "timezone", "agent", "prompt"}
	case "routine_update":
		fields = []string{"id", "name", "description", "schedule_kind", "schedule_expr", "timezone", "agent", "project_id", "prompt"}
		required = []string{"id"}
	case "routine_delete":
		fields = []string{"id"}
		required = fields
	case "wake_me":
		fields = []string{"at", "in", "note"}
		required = []string{"note"}
	}
	for _, f := range fields {
		props[f] = map[string]string{"type": "string"}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return b
}
func (t scheduleTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(t.Schema(), &schema)
	if _, err := stringArguments(raw, schema.Properties, schema.Required); err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.call == nil {
		return tool.Errorf("%s: scheduler unavailable", t.name), nil
	}
	text, err := t.call(ctx, c.SessionID, t.name, raw)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	return tool.Text(text), nil
}
