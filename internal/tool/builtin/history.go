package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// History supplies the stored conversation to the recall tools.
type History interface {
	SearchHistory(context.Context, string, store.HistoryFilter) ([]store.HistoryHit, error)
	BrowseHistory(context.Context, store.HistoryFilter, int64) ([]store.Entry, error)
	HistoryTurn(context.Context, string, int) ([]store.Entry, error)
	HistoryWindow(context.Context, string, int, int) ([]store.Entry, error)
	Entry(context.Context, string) (store.Entry, error)
	EntryBySequence(context.Context, string, int64) (store.Entry, error)
	Session(context.Context, string) (store.Session, error)
	SessionPath(context.Context, string) ([]store.Entry, error)
	Subagent(context.Context, string, string) (store.Session, error)
	Projects(context.Context) ([]store.Project, error)
}

type historyTool struct {
	name    string
	history History
}

func (t historyTool) Name() string   { return t.name }
func (t historyTool) Standalone()    {}
func (t historyTool) ReadOnly() bool { return true }
func (t historyTool) AgentOnly()     {}
func (t historyTool) Description() string {
	if t.name == "history_search" {
		return "Search all session history, or browse entries before the current context when query is empty. Results show nearby turns; use history_read for full content."
	}
	return "Read an entry or a window around an entry from history. Use the entry id from history_search; full returns untruncated content."
}
func (t historyTool) Schema() json.RawMessage {
	props := map[string]any{}
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	if t.name == "history_search" {
		props["query"] = text("Words to search for; omit or leave empty to browse compacted history.")
		props["since"] = text("Inclusive UTC date (YYYY-MM-DD) or RFC3339 time.")
		props["until"] = text("Inclusive UTC date (YYYY-MM-DD), or exclusive RFC3339 time.")
		props["project"] = text("Project slug.")
		props["page"] = map[string]any{"type": "integer", "minimum": 1, "description": "Page number, starting at 1; five groups per page."}
	} else {
		props["entry"] = text("Entry id, or # followed by its sequence number in the selected session.")
		props["around"] = text("Entry id at the centre of a window, or # followed by its sequence number.")
		props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
		props["full"] = map[string]any{"type": "boolean", "description": "Return complete entry content without truncation."}
	}
	props["subagent"] = text("Name of a subagent of this home session.")
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "additionalProperties": false})
	return b
}

type historyArgs struct {
	query, since, until, project, subagent, entry, around string
	page, limit                                           int
	full                                                  bool
}

func (t historyTool) arguments(raw json.RawMessage) (historyArgs, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(t.Schema(), &schema)
	fields, err := objectArguments(raw, schema.Properties, nil)
	if err != nil {
		return historyArgs{}, err
	}
	a := historyArgs{page: 1, limit: 10}
	for key, value := range fields {
		switch key {
		case "page", "limit":
			var n int
			if err := json.Unmarshal(value, &n); err != nil || string(value) == "null" || n < 1 || (key == "limit" && n > 100) || (key == "page" && n > 1000000) {
				return a, fmt.Errorf("invalid %s", key)
			}
			if key == "page" {
				a.page = n
			} else {
				a.limit = n
			}
		case "full":
			if string(value) != "true" && string(value) != "false" {
				return a, fmt.Errorf("full must be a boolean")
			}
			_ = json.Unmarshal(value, &a.full)
		default:
			v, err := stringArgument(key, value)
			if err != nil {
				return a, err
			}
			switch key {
			case "query":
				a.query = v
			case "since":
				a.since = v
			case "until":
				a.until = v
			case "project":
				a.project = v
			case "subagent":
				a.subagent = v
			case "entry":
				a.entry = v
			case "around":
				a.around = v
			}
		}
	}
	if t.name == "history_read" && (a.entry == "") == (a.around == "") {
		return a, fmt.Errorf("supply exactly one of entry or around")
	}
	return a, nil
}

func historyDate(value string, end bool) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	date, err := time.Parse("2006-01-02", value)
	if err == nil {
		if end {
			date = date.AddDate(0, 0, 1)
		}
		return &date, nil
	}
	date, err = time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("date must be YYYY-MM-DD or RFC3339")
	}
	date = date.UTC()
	return &date, nil
}

type historyEntry struct {
	ID        string          `json:"id"`
	Sequence  int64           `json:"sequence"`
	Session   string          `json:"session"`
	Time      time.Time       `json:"time"`
	Kind      store.EntryKind `json:"kind"`
	Content   string          `json:"content"`
	Truncated bool            `json:"truncated,omitempty"`
}

// historyText keeps the indexed text and tool results readable without exposing
// reasoning and model-call metrics as escaped JSON.
func historyText(e store.Entry) string {
	switch e.Kind {
	case store.KindUser, store.KindAssistant, store.KindToolResult:
		var m provider.Message
		if json.Unmarshal(e.Payload, &m) == nil {
			parts := []string{}
			if m.Content != "" {
				parts = append(parts, m.Content)
			}
			for _, call := range m.ToolCalls {
				parts = append(parts, strings.TrimSpace(call.Name+" "+string(call.Arguments)))
			}
			return strings.Join(parts, "\n")
		}
	case store.EntryKind("tool_call"):
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(e.Payload, &call) == nil {
			return strings.TrimSpace(call.Name + " " + string(call.Arguments))
		}
	case store.KindNotice, store.KindCompaction:
		var value struct {
			Text    string `json:"text"`
			Summary string `json:"summary"`
		}
		if json.Unmarshal(e.Payload, &value) == nil {
			if e.Kind == store.KindNotice {
				return value.Text
			}
			return value.Summary
		}
	}
	return string(e.Payload)
}

func historyView(e store.Entry, name string, full bool, query string) historyEntry {
	text := historyText(e)
	truncated := !full && len(text) > 2000
	if truncated {
		start := 0
		// Center the hit on the literal query when possible. PostgreSQL can match
		// stems instead, so a missing literal falls back to the start of the entry.
		if query != "" {
			if pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(strings.TrimSpace(query))); err == nil {
				if match := pattern.FindStringIndex(text); match != nil {
					start = max(0, match[0]-500)
				}
			}
		}
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		end := min(len(text), start+2000)
		for end > start && !utf8.ValidString(text[start:end]) {
			end--
		}
		text = text[start:end]
	}
	return historyEntry{ID: e.ID, Sequence: e.Seq, Session: name, Time: e.CreatedAt, Kind: e.Kind, Content: text, Truncated: truncated}
}

func historyName(s store.Session) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Title != "" {
		return s.Title
	}
	return s.ID
}

func (t historyTool) Call(ctx context.Context, call tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	a, err := t.arguments(raw)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.history == nil {
		return tool.Errorf("%s: history unavailable", t.name), nil
	}
	home, err := t.history.Session(ctx, call.SessionID)
	if err != nil || home.Kind != store.SessionHome {
		return tool.Errorf("%s: only home can read history", t.name), nil
	}
	selected := home
	if a.subagent != "" {
		selected, err = t.history.Subagent(ctx, home.ID, a.subagent)
		if err != nil {
			return tool.Errorf("%s: subagent not found", t.name), nil
		}
	}
	var result any
	if t.name == "history_search" {
		result, err = t.search(ctx, home, selected, a)
	} else {
		result, err = t.read(ctx, home, selected, a)
	}
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Text(string(data)), nil
}

const (
	maxHistoryGroupEntries = 8
	maxHistoryGroupBytes   = 8 * 1024
)

type historyGroup struct {
	Hit       string         `json:"hit"`
	Entries   []historyEntry `json:"entries"`
	Truncated bool           `json:"truncated,omitempty"`
}

func (t historyTool) search(ctx context.Context, home, selected store.Session, a historyArgs) (any, error) {
	since, err := historyDate(a.since, false)
	if err != nil {
		return nil, err
	}
	until, err := historyDate(a.until, true)
	if err != nil {
		return nil, err
	}
	if since != nil && until != nil && !since.Before(*until) {
		return nil, fmt.Errorf("since must be before until")
	}
	f := store.HistoryFilter{Since: since, Until: until, Limit: 5, Offset: (a.page - 1) * 5}
	if a.subagent != "" {
		f.SessionID = selected.ID
	}
	if a.project != "" {
		projects, err := t.history.Projects(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			if p.Slug == a.project {
				f.ProjectID = p.ID
				break
			}
		}
		if f.ProjectID == "" {
			return nil, fmt.Errorf("project not found")
		}
	}
	var entries []store.Entry
	if strings.TrimSpace(a.query) == "" {
		if f.ProjectID != "" && selected.ProjectID != f.ProjectID {
			return []historyGroup{}, nil
		}
		f.SessionID = selected.ID
		path, err := t.history.SessionPath(ctx, selected.ID)
		if err != nil {
			return nil, err
		}
		state, err := store.HistoryState(path, -1)
		if err != nil {
			return nil, err
		}
		before := state.FirstKept
		if state.Compactions == 0 {
			before = 0
		}
		if before == 0 && state.LastCompacted > 0 {
			before = state.LastCompacted + 1
		}
		entries, err = t.history.BrowseHistory(ctx, f, before)
		if err != nil {
			return nil, err
		}
	} else {
		hits, err := t.history.SearchHistory(ctx, a.query, f)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			entries = append(entries, hit.Entry)
		}
	}
	groups := make([]historyGroup, 0, len(entries))
	for _, e := range entries {
		sess := home
		if e.SessionID != home.ID {
			sess, err = t.history.Session(ctx, e.SessionID)
			if err != nil {
				return nil, err
			}
		}
		nearby, err := t.history.HistoryTurn(ctx, e.ID, maxHistoryGroupEntries+1)
		if err != nil {
			return nil, err
		}
		group := historyGroup{Hit: e.ID, Entries: make([]historyEntry, 0, maxHistoryGroupEntries)}
		name := historyName(sess)
		if len(name) > 256 {
			name = string([]rune(name)[:min(len([]rune(name)), 64)])
		}
		sort.SliceStable(nearby, func(i, j int) bool {
			di, dj := nearby[i].Seq-e.Seq, nearby[j].Seq-e.Seq
			if di < 0 {
				di = -di
			}
			if dj < 0 {
				dj = -dj
			}
			return di < dj
		})
		for _, n := range nearby {
			if len(group.Entries) == maxHistoryGroupEntries {
				group.Truncated = true
				break
			}
			query := ""
			if n.ID == e.ID {
				query = a.query
			}
			view := historyView(n, name, false, query)
			group.Entries = append(group.Entries, view)
			candidate := group
			candidate.Truncated = true
			data, err := json.Marshal(candidate)
			if err != nil {
				return nil, err
			}
			if len(data) > maxHistoryGroupBytes {
				group.Entries = group.Entries[:len(group.Entries)-1]
				group.Truncated = true
			}
		}
		sort.Slice(group.Entries, func(i, j int) bool { return group.Entries[i].Sequence < group.Entries[j].Sequence })
		groups = append(groups, group)
	}
	return groups, nil
}
func (t historyTool) read(ctx context.Context, home, selected store.Session, a historyArgs) (any, error) {
	id := a.entry
	if id == "" {
		id = a.around
	}
	var e store.Entry
	var err error
	if strings.HasPrefix(id, "#") {
		seq, parseErr := strconv.ParseInt(strings.TrimPrefix(id, "#"), 10, 64)
		if parseErr != nil || seq < 1 {
			return nil, fmt.Errorf("invalid entry number")
		}
		e, err = t.history.EntryBySequence(ctx, selected.ID, seq)
	} else {
		e, err = t.history.Entry(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	if a.subagent != "" && e.SessionID != selected.ID {
		return nil, fmt.Errorf("entry is not in the selected subagent")
	}
	sess := home
	if e.SessionID != home.ID {
		sess, err = t.history.Session(ctx, e.SessionID)
		if err != nil {
			return nil, err
		}
	}
	entries := []store.Entry{e}
	if a.around != "" {
		before := (a.limit - 1) / 2
		entries, err = t.history.HistoryWindow(ctx, e.ID, before, a.limit-1-before)
		if err != nil {
			return nil, err
		}
	}
	views := make([]historyEntry, 0, len(entries))
	for _, v := range entries {
		views = append(views, historyView(v, historyName(sess), a.full, ""))
	}
	return views, nil
}
