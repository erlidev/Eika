package servertest

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Contract is the API's wire contract: the bodies of every route, the error
// every failure answers with, and the payload of every event. Its JSON form
// is docs/api/contract.json.
type Contract struct {
	// Routes are keyed as routes.go registers them: `GET /api/projects/{id}`.
	Routes map[string]Route `json:"routes"`
	// Error is the body of every failure, and ErrorCodes the codes it names.
	Error      *Shape   `json:"error"`
	ErrorCodes []string `json:"error_codes"`
	// Events are each event type's payload; a type with none maps to nil.
	Events map[string]*Shape `json:"events"`
	// Types are the shapes of the recursive types the others Ref.
	Types map[string]*Shape `json:"types,omitempty"`
}

// Route is what one route reads and answers.
type Route struct {
	// Status is the status of a success.
	Status int `json:"status,omitempty"`
	// Request is the JSON body the route reads, or nil when it reads none.
	Request *Shape `json:"request,omitempty"`
	// RawRequest marks a route that reads its body as bytes, not JSON.
	RawRequest bool `json:"raw_request,omitempty"`
	// Response is the body of a success, or nil when a success has none.
	Response *Shape `json:"response,omitempty"`
	// WebSocket marks a route that upgrades; it has no JSON bodies.
	WebSocket bool `json:"websocket,omitempty"`
	// Public marks a route that needs no token.
	Public bool `json:"public,omitempty"`
}

// Load reads a contract file.
func Load(path string) (Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Contract{}, err
	}
	var c Contract
	if err := json.Unmarshal(data, &c); err != nil {
		return Contract{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return c, nil
}

// Match returns the key and route that serve a method and path. A literal
// segment wins over a `{parameter}`, as it does in routes.go.
func (c Contract) Match(method, path string) (string, Route, bool) {
	best, literals := "", -1
	for key := range c.Routes {
		n, ok := matches(key, method, path)
		if ok && (n > literals || n == literals && key < best) {
			best, literals = key, n
		}
	}
	if literals < 0 {
		return "", Route{}, false
	}
	return best, c.Routes[best], true
}

// matches reports whether key serves method and path, and how many of its
// segments are literals.
func matches(key, method, path string) (int, bool) {
	keyMethod, pattern, _ := strings.Cut(key, " ")
	if keyMethod != method {
		return 0, false
	}
	want, got := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return 0, false
	}
	literals := 0
	for i, segment := range want {
		if strings.HasPrefix(segment, "{") {
			if got[i] == "" {
				return 0, false
			}
			continue
		}
		if segment != got[i] {
			return 0, false
		}
		literals++
	}
	return literals, true
}

// CheckResponse reports every way a response to method and path breaks the
// contract: a success with another status or body, or a failure whose body
// is not the error shape. A path no route serves, and a body that is not
// JSON, such as the mux's own plain-text 405, are not the contract's to
// check.
func (c Contract) CheckResponse(method, path string, status int, contentType string, body []byte) []string {
	key, route, ok := c.Match(method, path)
	if !ok || route.WebSocket {
		return nil
	}
	var problems []string
	if status < 300 {
		if status != route.Status {
			problems = append(problems, fmt.Sprintf("%s: status %d, want %d", key, status, route.Status))
		}
		if route.Response == nil {
			if len(strings.TrimSpace(string(body))) > 0 {
				problems = append(problems, fmt.Sprintf("%s: a body, want none", key))
			}
			return problems
		}
		return append(problems, c.checkBody(key, route.Response, body)...)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		return problems
	}
	problems = append(problems, c.checkBody(key, c.Error, body)...)
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &failure) == nil && !slices.Contains(c.ErrorCodes, failure.Error.Code) {
		problems = append(problems, fmt.Sprintf("%s: error code %q is not one of %v", key, failure.Error.Code, c.ErrorCodes))
	}
	return problems
}

// CheckEvent reports every way an event's payload differs from its type's.
func (c Contract) CheckEvent(typ string, payload []byte) []string {
	shape, known := c.Events[typ]
	if !known {
		return []string{fmt.Sprintf("event %s: not in the contract", typ)}
	}
	if shape == nil {
		if len(payload) > 0 {
			return []string{fmt.Sprintf("event %s: a payload, want none", typ)}
		}
		return nil
	}
	return c.checkBody("event "+typ, shape, payload)
}

func (c Contract) checkBody(what string, shape *Shape, body []byte) []string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return []string{fmt.Sprintf("%s: body is not JSON: %v", what, err)}
	}
	problems := shape.Check(v, false, c.Types)
	for i, p := range problems {
		problems[i] = what + ": " + p
	}
	return problems
}
