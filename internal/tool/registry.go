package tool

import (
	"fmt"
	"sort"
	"sync"

	"github.com/erlidev/eika/internal/provider"
)

// Registry holds the tools one agent may call, keyed by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns a registry holding the given tools. Duplicate names are
// an error, because a tool the model cannot address unambiguously is a bug in
// the wiring.
func NewRegistry(tools ...Tool) (*Registry, error) {
	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds a tool.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("register tool: tool is nil")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("register tool: name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = map[string]Tool{}
	}
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("register tool %s: already registered", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool with the given name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns every registered tool, ordered by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}

// Filter returns a registry holding the tools keep accepts. It is how one run
// narrows the registry every run shares to the tools its session may call.
func (r *Registry) Filter(keep func(Tool) bool) *Registry {
	out := &Registry{tools: map[string]Tool{}}
	for _, t := range r.List() {
		if keep(t) {
			out.tools[t.Name()] = t
		}
	}
	return out
}

// Schemas describes every registered tool to a model, ordered by name.
func (r *Registry) Schemas() []provider.ToolDef {
	tools := r.List()
	out := make([]provider.ToolDef, 0, len(tools))
	for _, t := range tools {
		out = append(out, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return out
}
