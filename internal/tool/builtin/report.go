package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/tool"
)

// Reports takes the report a subagent ends an assignment with. The server
// binds it to the lifecycle service, which refuses a session that is not a
// subagent with an assignment running.
type Reports interface {
	Report(ctx context.Context, sessionID, summary string, outputs, followUps []string) error
}

// The bounds of a report, which the lifecycle service also enforces.
const (
	maxReportSummaryBytes = 32 << 10
	maxReportItemBytes    = 1 << 10
	maxReportItems        = 20
)

// reportTool is how a subagent hands its work back to Eika.
type reportTool struct{ reports Reports }

func (reportTool) Name() string { return "report" }
func (reportTool) Standalone()  {}
func (reportTool) AgentOnly()   {}

func (reportTool) Description() string {
	return "Report back to Eika. Call it once, last, when the assignment is done or you can go no further: " +
		"the summary is what Eika reads, so say what you found or did, what you assumed, and what is unresolved. " +
		"List what you produced under outputs (files, links, figures) and what Eika should do next under follow_ups."
}

func (reportTool) Schema() json.RawMessage {
	list := func(description string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maxReportItems, "description": description}
	}
	b, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":    map[string]any{"type": "string", "description": "The outcome, starting with one line that says it, then the detail Eika needs."},
			"outputs":    list("What you produced: file paths, URLs, figures. Optional."),
			"follow_ups": list("What Eika should do next, if anything. Optional."),
		},
		"required":             []string{"summary"},
		"additionalProperties": false,
	})
	return b
}

func (t reportTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(t.Schema(), &schema)
	summary, outputs, followUps, err := reportArguments(raw, schema.Properties, schema.Required)
	if err != nil {
		return tool.Errorf("report: %v", err), nil
	}
	if t.reports == nil {
		return tool.Errorf("report: reports are unavailable"), nil
	}
	if err := t.reports.Report(ctx, c.SessionID, summary, outputs, followUps); err != nil {
		return tool.Errorf("report: %v", err), nil
	}
	return tool.Text("Reported. Your assignment is done; reply with one short line to end it."), nil
}

// reportArguments decodes a report's arguments, refusing text that decoding
// would alter and lists past the bounds the tool tells the model about.
func reportArguments(raw []byte, allowed map[string]json.RawMessage, required []string) (summary string, outputs, followUps []string, err error) {
	fields, err := objectArguments(raw, allowed, required)
	if err != nil {
		return "", nil, nil, err
	}
	if summary, err = stringArgument("summary", fields["summary"]); err != nil {
		return "", nil, nil, err
	}
	if strings.TrimSpace(summary) == "" || len(summary) > maxReportSummaryBytes || strings.ContainsRune(summary, 0) {
		return "", nil, nil, fmt.Errorf("summary must be nonempty, without NUL, and at most %d bytes", maxReportSummaryBytes)
	}
	if outputs, err = reportList("outputs", fields["outputs"]); err != nil {
		return "", nil, nil, err
	}
	if followUps, err = reportList("follow_ups", fields["follow_ups"]); err != nil {
		return "", nil, nil, err
	}
	return summary, outputs, followUps, nil
}

// reportList decodes an optional list of short strings.
func reportList(key string, v json.RawMessage) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(v, &items) != nil || items == nil {
		return nil, fmt.Errorf("%s must be a list of strings", key)
	}
	if len(items) > maxReportItems {
		return nil, fmt.Errorf("%s must have at most %d entries", key, maxReportItems)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, err := stringArgument(key, item)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(s) == "" || len(s) > maxReportItemBytes || strings.ContainsRune(s, 0) {
			return nil, fmt.Errorf("each of %s must be nonempty, without NUL, and at most %d bytes", key, maxReportItemBytes)
		}
		out = append(out, s)
	}
	return out, nil
}

var _ tool.AgentOnly = reportTool{}
