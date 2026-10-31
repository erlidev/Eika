package server

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/erlidev/eika/internal/store"
)

// settingModelClasses assigns each model class the name of the model that
// serves it. A class it assigns none falls back (modelClass.fallback).
const settingModelClasses = "model_classes"

// modelClass is the role a model plays for the harness. Harness tasks and,
// from the agents phase, agents name a class and never a model; the
// model_classes setting maps each class to a model row.
type modelClass string

// The classes (docs/design/runtime.md section 1).
const (
	// classDeep is hard reasoning, and may be a cloud model.
	classDeep modelClass = "deep"
	// classMain is the main agent and most tasks.
	classMain modelClass = "main"
	// classFast is titles and other small tasks.
	classFast modelClass = "fast"
	// classDecision is Sentry's reviewer and monitor, screening and routing.
	// Nothing asks for it before phase 4.
	classDecision modelClass = "decision"
)

// modelClasses is every class, in the order the UI lists them.
var modelClasses = []modelClass{classDeep, classMain, classFast, classDecision}

// known reports whether c is a class.
func (c modelClass) known() bool {
	return slices.Contains(modelClasses, c)
}

// kind is the kind of model that serves the class: a decision model for the
// decision class and a chat model for every other.
func (c modelClass) kind() string {
	if c == classDecision {
		return store.ModelKindDecision
	}
	return store.ModelKindChat
}

// fallback is the class that serves c when it has no model of its own, and
// false when nothing does. The decision class has none here: a chat model
// emulating the protocol (sentry.md 9.3) is built with the first thing that
// asks for it.
func (c modelClass) fallback() (modelClass, bool) {
	switch c {
	case classDeep, classFast:
		return classMain, true
	}
	return "", false
}

// classChoice is the model a class resolved to.
type classChoice struct {
	Model store.Model
	// Via is the class whose assignment supplied the model: the class asked
	// for, or the one it fell back to. It is empty when ViaDefault is set.
	Via modelClass
	// ViaDefault says the model is the default model, which a chat class
	// reaches when neither it nor the classes it falls back to have a model.
	ViaDefault bool
}

// fellBack reports whether the model came from anything but the assignment of
// the class asked for.
func (c classChoice) fellBack(asked modelClass) bool {
	return c.ViaDefault || c.Via != asked
}

// resolveClass returns the model class c is served by: the model assigned to
// it, else the one assigned to the class it falls back to, and for a chat
// class at the end of that chain the default model (defaultModel, the name of
// the chat model a run uses when none is named; empty for none). An assignment
// that names no model of the class's kind, such as a deleted model, counts as
// none. It reports false when nothing in the chain has a model.
func resolveClass(c modelClass, assigned map[modelClass]string, models []store.Model, defaultModel string) (classChoice, bool) {
	kind := c.kind()
	for {
		if i := slices.IndexFunc(models, func(m store.Model) bool {
			return assigned[c] != "" && m.Name == assigned[c] && m.Kind == c.kind()
		}); i >= 0 {
			return classChoice{Model: models[i], Via: c}, true
		}
		next, ok := c.fallback()
		if !ok {
			break
		}
		c = next
	}
	if kind == store.ModelKindChat && defaultModel != "" {
		if i := slices.IndexFunc(models, func(m store.Model) bool {
			return m.Name == defaultModel && m.Kind == store.ModelKindChat
		}); i >= 0 {
			return classChoice{Model: models[i], ViaDefault: true}, true
		}
	}
	return classChoice{}, false
}

// classAssignments reads the model_classes setting: the model name each class
// is assigned, and an empty map when the setting holds none.
func (s *Server) classAssignments(ctx context.Context) map[modelClass]string {
	var assigned map[modelClass]string
	readSetting(ctx, s.deps.Store, s.log, settingModelClasses, &assigned)
	return assigned
}

// defaultModelName is the name of the default model among models, as a run
// resolves it, and empty when there is no chat model.
func (s *Server) defaultModelName(ctx context.Context, models []store.Model) string {
	m, _ := s.defaultModel(ctx, models)
	return m.Name
}

// viaDefault is the Via of a class served by the default model.
const viaDefault = "default"

// classBody is one model class and its model, on the wire.
type classBody struct {
	Class string `json:"class"`
	// Kind is the kind of model the class takes: chat, or decision.
	Kind string `json:"kind"`
	// Model is the name of the model that serves the class, and empty when
	// none does.
	Model string `json:"model"`
	// Via is the class whose assignment supplied Model when that is not this
	// one, which is a fallback, or "default" when the default model did.
	// Empty when the class has its own model or none.
	Via string `json:"via,omitempty"`
	// ProviderID and Pool are the endpoint and the hardware group Model is
	// served from. Classes with one Pool contend for its slots.
	ProviderID string `json:"provider_id,omitempty"`
	Pool       string `json:"pool,omitempty"`
}

// classBodies resolves every class, in the order the UI lists them.
func (s *Server) classBodies(ctx context.Context) ([]classBody, error) {
	models, err := s.deps.Store.Models(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := s.deps.Store.Providers(ctx)
	if err != nil {
		return nil, err
	}
	assigned, def := s.classAssignments(ctx), s.defaultModelName(ctx, models)
	out := make([]classBody, 0, len(modelClasses))
	for _, c := range modelClasses {
		body := classBody{Class: string(c), Kind: c.kind()}
		if choice, ok := resolveClass(c, assigned, models, def); ok {
			body.Model, body.ProviderID = choice.Model.Name, choice.Model.ProviderID
			switch {
			case choice.ViaDefault:
				body.Via = viaDefault
			case choice.fellBack(c):
				body.Via = string(choice.Via)
			}
			if i := slices.IndexFunc(providers, func(p store.Provider) bool { return p.ID == choice.Model.ProviderID }); i >= 0 {
				body.Pool = providers[i].PoolName()
			}
		}
		out = append(out, body)
	}
	return out, nil
}

// validateModelClasses rejects a model_classes value that names a class the
// harness does not have, a model that does not exist, or a model of another
// kind than the class serves.
func (s *Server) validateModelClasses(ctx context.Context, value json.RawMessage) error {
	var assigned map[modelClass]string
	if err := json.Unmarshal(value, &assigned); err != nil {
		return invalidf("%s must be an object of class names to model names", settingModelClasses)
	}
	for c, name := range assigned {
		if !c.known() {
			return invalidf("%s names %q, which is not a class", settingModelClasses, string(c))
		}
		if name == "" {
			continue
		}
		m, err := s.modelNamed(ctx, settingModelClasses, name)
		if err != nil {
			return err
		}
		if m.Kind != c.kind() {
			return invalidf("%s assigns %q, a %s model, to the %s class, which needs a %s model",
				settingModelClasses, name, m.Kind, c, c.kind())
		}
	}
	return nil
}
