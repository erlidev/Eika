package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// Subagents runs the named subagents of the calling home session. The server
// binds it to the lifecycle service and refuses any other caller, so a
// subagent cannot delegate.
type Subagents interface {
	// StartSubagent reports whether it started work; an existing name
	// returns that subagent unchanged.
	StartSubagent(ctx context.Context, home string, child store.Session) (store.Session, bool, error)
	// MessageSubagent reports whether the subagent took the text; a running
	// one returns its status without it.
	MessageSubagent(ctx context.Context, home, name, text, questionID string) (store.Session, bool, error)
	// Subagents returns one subagent by name, or all of them for "".
	Subagents(ctx context.Context, home, name string) ([]store.Session, error)
	CancelSubagent(ctx context.Context, home, name string) (store.Session, error)
}

// subagentName is a handle such as plumber or flights-lisbon.
var subagentName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// subagentTool is one of Eika's delegation tools.
type subagentTool struct {
	name      string
	subagents Subagents
	projects  Projects
}

func (t subagentTool) Name() string { return t.name }

// ReadOnly is true for subagent_status alone.
func (t subagentTool) ReadOnly() bool { return t.name == "subagent_status" }
func (t subagentTool) Standalone()    {}
func (t subagentTool) AgentOnly()     {}

func (t subagentTool) Description() string {
	switch t.name {
	case "subagent_start":
		return "Start a named subagent on a brief. It works in the background, in its own context, and this call returns at once; its report comes back to you as a notice, and subagent_status has it too. " +
			"A worker works on the home computer, in the project's directory when you name a project; a researcher reads the web and has no computer. " +
			"A name already in use returns that subagent's status instead: give it more work with subagent_message."
	case "subagent_message":
		return "Answer a waiting question with its question_id from the notice, or send a message to a subagent that is not running: it resumes in its own context with the text. A running subagent does not take the text; wait for it or cancel it."
	case "subagent_status":
		return "Without a name, list every subagent with its state and the first line of its last report. With a name, also return its brief and full last report."
	default:
		return "Stop a subagent's current run. It keeps its context and can be messaged again."
	}
}

func (t subagentTool) Schema() json.RawMessage {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	name := text("The subagent's handle: lowercase letters, digits and single hyphens, such as flights-lisbon.")
	var props map[string]any
	var required []string
	switch t.name {
	case "subagent_start":
		props = map[string]any{
			"agent":   map[string]any{"type": "string", "enum": []string{"researcher", "worker"}, "description": "researcher reads the web; worker works on a computer."},
			"name":    name,
			"brief":   text("Everything the subagent needs: the goal, what is known, constraints and what to report. It decides small details itself and states assumptions in its report. When unclear intent would change the result, ask_user sends you its question as a notice."),
			"project": text("Optional project slug. The subagent gets the project's instructions, and a worker works in its directory."),
		}
		required = []string{"agent", "name", "brief"}
	case "subagent_message":
		props = map[string]any{"subagent": name, "text": text("The answer to its waiting question, or a message such as new work or a correction."), "question_id": text("Required when answering a question: use the id in its notice. Omit for new work.")}
		required = []string{"subagent", "text"}
	case "subagent_status":
		props = map[string]any{"subagent": name}
		required = []string{}
	default:
		props = map[string]any{"subagent": name}
		required = []string{"subagent"}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return b
}

func (t subagentTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(t.Schema(), &schema)
	args, err := stringArguments(raw, schema.Properties, schema.Required)
	if err == nil {
		err = checkSubagentArguments(args)
	}
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.subagents == nil || t.projects == nil {
		return tool.Errorf("%s: subagents are unavailable", t.name), nil
	}
	projects, err := t.projects.Projects(ctx)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	slugs := make(map[string]string, len(projects))
	for _, p := range projects {
		slugs[p.ID] = p.Slug
	}
	var (
		rows []store.Session
		row  store.Session
		ok   bool
		note string
	)
	switch t.name {
	case "subagent_start":
		child := store.Session{Agent: args["agent"], Name: args["name"], Brief: args["brief"]}
		if slug, named := args["project"]; named {
			for _, p := range projects {
				if p.Slug == slug {
					child.ProjectID = p.ID
				}
			}
			if child.ProjectID == "" {
				return tool.Errorf("%s: project %q not found", t.name, slug), nil
			}
		}
		row, ok, err = t.subagents.StartSubagent(ctx, c.SessionID, child)
		note = "Started. It works in the background, and its report comes back as a notice; check on it with subagent_status."
		if !ok {
			note = "A subagent with this name already exists and was not changed. Give it more work with subagent_message."
		}
	case "subagent_message":
		row, ok, err = t.subagents.MessageSubagent(ctx, c.SessionID, args["subagent"], args["text"], args["question_id"])
		note = "Accepted your message. It continues in its own context."
		if ok && args["question_id"] == "" {
			note = "Accepted your message. No question is waiting; it continues in its own context."
		}
		switch {
		case !ok && args["question_id"] != "":
			note = "That question is no longer pending. The message was not accepted."
		case !ok && row.State == store.SessionWaiting:
			note = "A question is waiting. Include its question_id from the notice; the message was not accepted."
		case !ok:
			note = "It is running and did not take your message. No question is waiting. Wait for it, or cancel it and message it again."
		}
	case "subagent_status":
		rows, err = t.subagents.Subagents(ctx, c.SessionID, args["subagent"])
	default:
		row, err = t.subagents.CancelSubagent(ctx, c.SessionID, args["subagent"])
		note = "Its run is stopped. Message it to resume."
	}
	if errors.Is(err, store.ErrNotFound) {
		return tool.Errorf("%s: no subagent is named %q", t.name, args["subagent"]), nil
	}
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.name != "subagent_status" {
		out := subagentView(row, slugs, false)
		out["note"] = note
		return jsonResult(out), nil
	}
	if args["subagent"] != "" && len(rows) == 1 {
		return jsonResult(subagentView(rows[0], slugs, true)), nil
	}
	board := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		board = append(board, subagentView(r, slugs, false))
	}
	return jsonResult(board), nil
}

// checkSubagentArguments applies the bounds the tools tell the model about.
func checkSubagentArguments(args map[string]string) error {
	for _, field := range []string{"name", "subagent"} {
		if v, ok := args[field]; ok && (len(v) > maxSubagentNameBytes || !subagentName.MatchString(v)) {
			return fmt.Errorf("%s must have 1 to %d lowercase letters, digits or single separating hyphens", field, maxSubagentNameBytes)
		}
	}
	for _, field := range []string{"brief", "text"} {
		if v, ok := args[field]; ok && (strings.TrimSpace(v) == "" || len(v) > maxSubagentTextBytes || strings.ContainsRune(v, 0)) {
			return fmt.Errorf("%s must be nonempty, without NUL, and at most %d bytes", field, maxSubagentTextBytes)
		}
	}
	if v, ok := args["question_id"]; ok && (strings.TrimSpace(v) == "" || len(v) > 128 || strings.ContainsRune(v, 0)) {
		return fmt.Errorf("question_id must be nonempty, without NUL, and at most 128 bytes")
	}
	if v, ok := args["agent"]; ok && v != "researcher" && v != "worker" {
		return fmt.Errorf("agent must be researcher or worker")
	}
	return nil
}

// subagentView is what Eika sees of one subagent: the board's fields, and with
// detail its brief and whole last report instead of the report's first line.
func subagentView(s store.Session, slugs map[string]string, detail bool) map[string]any {
	out := map[string]any{"name": s.Name, "agent": s.Agent, "state": s.State}
	if s.ProjectID != "" {
		out["project"] = slugs[s.ProjectID]
	}
	if s.WaitingReason != "" {
		if s.State == store.SessionFailed || s.State == store.SessionCancelled {
			out["ended_because"] = s.WaitingReason
		} else {
			out["waiting_on"] = s.WaitingReason
		}
	}
	if !s.LastActiveAt.IsZero() {
		out["last_active_at"] = s.LastActiveAt.UTC().Format(time.RFC3339)
	}
	if detail {
		out["brief"] = s.Brief
		out["report"] = s.Result
	} else if line := sentry.ReportSummary(s.Result); line != "" {
		out["report"] = line
	}
	return out
}

func jsonResult(v any) tool.Result {
	b, _ := json.Marshal(v)
	return tool.Text(string(b))
}

var _ tool.AgentOnly = subagentTool{}
