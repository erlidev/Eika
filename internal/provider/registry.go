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
}

// Constructor builds a Provider for one endpoint.
type Constructor func(e Endpoint) (Provider, error)

// Registry maps a provider kind name to its constructor. Registration is
// explicit: NewRegistry is called once during wiring, never from init.
type Registry struct {
	kinds map[string]Constructor
}

// NewRegistry returns a registry holding every provider kind Eika supports.
// Constructors are parameters because every provider package imports this one,
// so this file cannot import them back. Adding a provider means adding a
// parameter and one map entry here.
func NewRegistry(openAI Constructor) *Registry {
	return &Registry{kinds: map[string]Constructor{
		"openai": openAI,
	}}
}

// Build returns a Provider of the given kind on an endpoint.
func (r *Registry) Build(kind string, e Endpoint) (Provider, error) {
	c, ok := r.kinds[kind]
	if !ok {
		return nil, fmt.Errorf("build provider: unknown kind %q", kind)
	}
	return c(e)
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
