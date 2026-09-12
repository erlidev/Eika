package provider

import (
	"fmt"
	"sort"

	"github.com/erlidev/eika/internal/config"
)

// Constructor builds a Provider for one configured model. It resolves the
// model's credentials and fails when they are missing.
type Constructor func(m config.Model) (Provider, error)

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

// Build returns a Provider of the given kind for a configured model.
func (r *Registry) Build(kind string, m config.Model) (Provider, error) {
	c, ok := r.kinds[kind]
	if !ok {
		return nil, fmt.Errorf("build provider: unknown kind %q", kind)
	}
	return c(m)
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
