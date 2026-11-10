package provider

import (
	"fmt"
	"sort"
)

// Endpoint is where one provider the user configured is reached and how it
// authenticates. It is built from a provider row for as long as one call
// needs it; the key is never logged or returned to a client.
type Endpoint struct {
	// BaseURL is the API root, such as https://api.openai.com/v1.
	BaseURL string
	// APIKey is the credential. It may be empty for a local endpoint that
	// asks for none.
	APIKey string
	// API is the protocol to speak to the endpoint; empty means APIChat.
	API API
	// Hosted reports a hosted API rather than a server on the user's own
	// hardware. A hosted API takes the Responses fields the specification
	// defines and no others, and returns reasoning only as encrypted content
	// it is asked for; a local server takes sampling fields of its own beside
	// them.
	Hosted bool
	// SlotHints reports that the server takes a request naming the slot it
	// should run on (llama.cpp with several slots). Only then does a provider
	// send a request's SlotHint; to any other server the field is unknown and
	// may be refused.
	SlotHints bool
}

// Constructor builds a Provider for one endpoint.
type Constructor func(e Endpoint) (Provider, error)

// kind is the constructors of one provider kind, one per protocol it speaks.
// A protocol the kind does not speak has none.
type kind struct {
	chat, responses Constructor
}

// Registry maps a provider kind name to its constructors. Registration is
// explicit: NewRegistry is called once during wiring, never from init.
type Registry struct {
	kinds map[string]kind
}

// NewRegistry returns a registry holding every provider kind Eika supports.
// Constructors are parameters because every provider package imports this one,
// so this file cannot import them back. Adding a provider means adding a
// parameter and one map entry here.
func NewRegistry(openAIChat, openAIResponses Constructor) *Registry {
	return &Registry{kinds: map[string]kind{
		"openai": {chat: openAIChat, responses: openAIResponses},
	}}
}

// Build returns a Provider of the given kind on an endpoint, speaking the
// protocol the endpoint names.
func (r *Registry) Build(k string, e Endpoint) (Provider, error) {
	c, ok := r.kinds[k]
	if !ok {
		return nil, fmt.Errorf("build provider: unknown kind %q", k)
	}
	api, construct := e.API, c.chat
	switch e.API {
	case "":
		api = APIChat
	case APIChat:
	case APIResponses:
		construct = c.responses
	default:
		return nil, fmt.Errorf("build provider: unknown api %q", e.API)
	}
	if construct == nil {
		return nil, fmt.Errorf("build provider: kind %q does not speak the %s api", k, api)
	}
	return construct(e)
}

// Kinds lists the registered provider kinds in sorted order.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.kinds))
	for k := range r.kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
