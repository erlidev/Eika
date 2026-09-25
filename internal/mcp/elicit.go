package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
)

// ErrNoElicitation reports that nothing waits for an answer to the given
// elicitation: it was answered, or its call ended.
var ErrNoElicitation = errors.New("elicitation not found")

// ErrBadElicitationAnswer reports an answer an elicitation does not accept.
var ErrBadElicitationAnswer = errors.New("invalid elicitation answer")

// Elicitations holds what MCP servers are asking the user during tool
// calls. A call registers one and waits; the server answers it from an HTTP
// request. One broker is shared by every run, as the ask_user questions are.
type Elicitations struct {
	emit event.Emitter

	mu      sync.Mutex
	pending map[string]*pendingElicitation
}

// NewElicitations returns an empty broker that announces what it is asked on
// emit.
func NewElicitations(emit event.Emitter) *Elicitations {
	if emit == nil {
		emit = event.Discard
	}
	return &Elicitations{emit: emit, pending: map[string]*pendingElicitation{}}
}

// Elicitation is one unanswered request for input, as the API reports it.
type Elicitation struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	CallID    string `json:"call_id"`
	// Server is the name of the MCP server that asks.
	Server          string          `json:"server"`
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
	URL             string          `json:"url,omitempty"`
	AskedAt         time.Time       `json:"asked_at"`
}

type pendingElicitation struct {
	Elicitation
	answer chan ElicitResult
}

// ask registers a request for input, announces it, and waits for the
// answer. A request the UI could not show is declined without asking.
func (e *Elicitations) ask(ctx context.Context, asked Elicitation, req ElicitRequest) (ElicitResult, error) {
	asked.Mode, asked.Message = req.Mode, req.Message
	if asked.Mode == "" {
		asked.Mode = "form"
	}
	switch asked.Mode {
	case "form":
		asked.RequestedSchema = req.RequestedSchema
		if !flatSchema(req.RequestedSchema) {
			return ElicitResult{Action: ElicitDecline}, nil
		}
	case "url":
		u, err := url.Parse(req.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return ElicitResult{Action: ElicitDecline}, nil
		}
		asked.URL = u.String()
	default:
		return ElicitResult{Action: ElicitDecline}, nil
	}
	asked.ID = newElicitationID()
	asked.AskedAt = time.Now().UTC()
	p := &pendingElicitation{Elicitation: asked, answer: make(chan ElicitResult, 1)}
	e.mu.Lock()
	e.pending[asked.ID] = p
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.pending, asked.ID)
		e.mu.Unlock()
	}()

	if ev, err := event.New(event.TypeMCPElicitation, event.SessionTopic(asked.SessionID), event.MCPElicitation{
		ElicitationID:   asked.ID,
		RunID:           asked.RunID,
		SessionID:       asked.SessionID,
		CallID:          asked.CallID,
		Server:          asked.Server,
		Mode:            asked.Mode,
		Message:         asked.Message,
		RequestedSchema: asked.RequestedSchema,
		URL:             asked.URL,
	}); err == nil {
		e.emit.Emit(ctx, ev)
	}
	select {
	case a := <-p.answer:
		return a, nil
	case <-ctx.Done():
		return ElicitResult{}, ctx.Err()
	}
}

// Answer delivers the user's answer to a waiting elicitation.
func (e *Elicitations) Answer(id string, res ElicitResult) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.pending[id]
	if !ok {
		return fmt.Errorf("answer elicitation %s: %w", id, ErrNoElicitation)
	}
	if err := p.accepts(res); err != nil {
		return fmt.Errorf("answer elicitation %s: %w: %v", id, ErrBadElicitationAnswer, err)
	}
	delete(e.pending, id)
	p.answer <- res
	return nil
}

// Pending returns the elicitations waiting for an answer, oldest first.
func (e *Elicitations) Pending() []Elicitation {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Elicitation, 0, len(e.pending))
	for _, p := range e.pending {
		out = append(out, p.Elicitation)
	}
	slices.SortFunc(out, func(a, b Elicitation) int { return a.AskedAt.Compare(b.AskedAt) })
	return out
}

// accepts checks an answer against what was asked: an action, and for an
// accepted form, content the schema allows.
func (p *pendingElicitation) accepts(res ElicitResult) error {
	switch res.Action {
	case ElicitDecline, ElicitCancel:
		if len(res.Content) > 0 && string(res.Content) != "null" {
			return errors.New("only an accepted answer carries content")
		}
		return nil
	case ElicitAccept:
	default:
		return fmt.Errorf("the action must be %s, %s, or %s", ElicitAccept, ElicitDecline, ElicitCancel)
	}
	if p.Mode == "url" {
		if len(res.Content) > 0 && string(res.Content) != "null" {
			return errors.New("a URL elicitation is accepted without content")
		}
		return nil
	}
	return checkFormContent(p.RequestedSchema, res.Content)
}

// formSchema is the flat schema a form elicitation carries.
type formSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
}

// formField is what the client reads of one field's schema.
type formField struct {
	Type  string `json:"type"`
	Items *struct {
		Type string `json:"type"`
	} `json:"items"`
}

// flatSchema reports whether a form schema is one the UI can render: an
// object whose properties are strings, numbers, booleans, or arrays of
// strings.
func flatSchema(raw json.RawMessage) bool {
	var s formSchema
	if json.Unmarshal(raw, &s) != nil || s.Type != "object" {
		return false
	}
	for _, prop := range s.Properties {
		var f formField
		if json.Unmarshal(prop, &f) != nil {
			return false
		}
		switch f.Type {
		case "string", "number", "integer", "boolean":
		case "array":
			if f.Items == nil || (f.Items.Type != "string" && f.Items.Type != "") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// checkFormContent checks accepted form content against its schema: an
// object with the required fields, each of the type its property says.
func checkFormContent(schema, content json.RawMessage) error {
	var s formSchema
	if err := json.Unmarshal(schema, &s); err != nil {
		return errors.New("the form's schema is unreadable")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(content, &values); err != nil || values == nil {
		return errors.New("an accepted form carries its content as an object")
	}
	for _, name := range s.Required {
		if _, ok := values[name]; !ok {
			return fmt.Errorf("%s is required", name)
		}
	}
	for name, raw := range values {
		prop, ok := s.Properties[name]
		if !ok {
			return fmt.Errorf("the form has no field %s", name)
		}
		var f formField
		_ = json.Unmarshal(prop, &f)
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return fmt.Errorf("%s is not a JSON value", name)
		}
		valid := false
		switch x := v.(type) {
		case string:
			valid = f.Type == "string"
		case float64:
			valid = f.Type == "number" || (f.Type == "integer" && x == float64(int64(x)))
		case bool:
			valid = f.Type == "boolean"
		case []any:
			valid = f.Type == "array"
			for _, item := range x {
				if _, isString := item.(string); !isString {
					valid = false
				}
			}
		}
		if !valid {
			return fmt.Errorf("%s must be a %s", name, f.Type)
		}
	}
	return nil
}

// newElicitationID returns an identifier for one elicitation.
func newElicitationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("el-%d", time.Now().UnixNano())
	}
	return "el-" + hex.EncodeToString(b[:])
}
