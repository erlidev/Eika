// Package sentry is Eika's permission authority. Core calls one interface, the
// Gate: Decide before a tool runs and Ingest on every untrusted result before
// it reaches a model. The package depends on no other part of Core, and
// internal/imports_test.go keeps it from importing server, agent or computer.
// Core passes in what it needs, such as the audit log, as an interface here.
// Design: docs/design/sentry.md.
package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Gate is what Core calls. Phase 4b adds Observe.
type Gate interface {
	// Decide answers at once. An error means the gate could not answer: the
	// caller blocks side-effecting tools and lets the rest run.
	Decide(ctx context.Context, a Action) (Verdict, error)
	// Ingest redacts, screens and wraps untrusted content before it reaches a
	// model, and returns the text to use in its place, one entry per segment.
	// A tool result that is one piece of content is one segment; a web search
	// is one segment per result, so a removal never spans two results.
	Ingest(ctx context.Context, segments []Segment) ([]Ingested, error)
	// Halted reports whether Eika is halted, in memory. While it is, Decide
	// denies every action.
	Halted() HaltState
	// Halt and Resume change the persisted state and write the audit row. The
	// bool is false when the state was already what was asked for. Aborting
	// runs and telling the user are Core's part, not the gate's.
	Halt(ctx context.Context, reason string) (HaltState, bool, error)
	Resume(ctx context.Context) (HaltState, bool, error)
}

// Kind says how an Action's Name is read.
type Kind string

// The kinds of action.
const (
	KindTool   Kind = "tool"   // Name is a tool name
	KindPlugin Kind = "plugin" // Name is plugin/tool
	KindEgress Kind = "egress" // Name is a host
)

// Principal is who acts. Fields that do not apply are empty.
type Principal struct {
	Session  string `json:"session"`
	Run      string `json:"run,omitempty"`
	Subagent string `json:"subagent,omitempty"`
	Routine  string `json:"routine,omitempty"`
	Computer string `json:"computer,omitempty"`
}

// Action describes one consequential thing in one shape. There is no intent
// field: the policy looks at what the action is and where it reaches.
type Action struct {
	// Key is stable: the session id and the loop's tool-call id (never the
	// backend's), or the container and connection id for egress.
	Key       string
	Principal Principal
	Kind      Kind
	Name      string
	// Args are the call's arguments, or a digest for egress.
	Args json.RawMessage
	// Destination is where the action reaches outside Eika, if anywhere: the
	// URL of a fetch, the host of a connection, a plugin's recipient.
	Destination string
}

// Decision is the outcome class of a verdict.
type Decision string

// The decisions a verdict can carry.
const (
	Allow Decision = "allow" // the call proceeds
	Ask   Decision = "ask"   // phase 11: the user answers; until then Core treats it as Deny
	Deny  Decision = "deny"  // the tool returns "Blocked: <Reason>"
)

// Verdict is Decide's answer.
type Verdict struct {
	Decision Decision
	// Reason is one plain sentence for the model when the call is not allowed.
	// It never names Sentry, rules or grants.
	Reason string
	// Detail is event-specific and goes to the audit row: the kind of
	// credential found, never its value.
	Detail string
}

// SourceKind names where untrusted content came from.
type SourceKind string

// The sources of untrusted content.
const (
	SourceFetch   SourceKind = "web_fetch"
	SourceSearch  SourceKind = "web_search" // one result, not a page of them
	SourceReport  SourceKind = "report"     // a subagent's report or question: screened, not wrapped
	SourceHistory SourceKind = "history"    // a child session's raw transcript, read through the history tools: screened and wrapped
	SourcePlugin  SourceKind = "plugin"     // phase 11
)

// Source says where ingested content came from, so a removal can be recorded
// against its session and tool call.
type Source struct {
	Kind    SourceKind
	Session string
	Run     string
	Key     string // the tool call's key, when there is one
	Name    string // the URL, query or plugin tool
}

// Segment is one piece of untrusted content with its own source.
type Segment struct {
	Source Source
	Text   string
}

// Ingested is a segment after ingest. Removed counts the removals in it, which
// is what marks a tool card as having had one.
type Ingested struct {
	Text    string
	Removed int
}

// Audit event names. Plain allows are not audited.
const (
	EventDeny    = "deny"
	EventAsk     = "ask"
	EventRemoval = "removal"
)

// AuditEntry is one append-only row of the audit log. It holds the action and
// its digest, never secret values.
type AuditEntry struct {
	ID        string
	At        time.Time
	Event     string
	Principal Principal
	Key       string
	Kind      string
	Name      string
	Digest    string // of the arguments, so a row can be matched to a call without them
	Reason    string
	Detail    json.RawMessage // event-specific JSON, or empty
}

// AuditLog stores audit rows. Core supplies it (store.Store implements it).
type AuditLog interface {
	AppendAudit(ctx context.Context, e AuditEntry) error
}

// Options are the parts a gate is built from.
type Options struct {
	// Audit receives every decision that is not a plain allow, and every removal.
	Audit AuditLog
	// Withheld receives the text of every removal. Nil keeps no record.
	Withheld WithheldLog
	// Halts keeps the halted state. Nil means the state is never persisted and
	// Halt returns an error.
	Halts HaltStore
	// Screen is the injection screen. Nil means no screen is configured, and
	// content passes unscreened.
	Screen Screener
	// Threshold is the score at or over which a window is removed.
	Threshold float64
	// Canary returns the planted fake key, or "" while there is none. A call
	// whose arguments hold it halts Eika.
	Canary func() string
	// Halter halts Eika for a canary hit, with everything a halt does beyond
	// the gate's own state: Core aborts runs and tells the user. Nil halts the
	// gate alone.
	Halter func(ctx context.Context, reason string) error
	// ScreenSettings returns the current threshold and whether screening is required.
	ScreenSettings func(context.Context) (float64, bool, error)
	// GuardChanged reports a transition in guard health.
	GuardChanged func(bool)
	// Log reports a screen that did not answer. Nil uses slog.Default.
	Log *slog.Logger
}

// New returns the gate for a deployment. Until later packages add rules it
// allows every action. Ingest strips invisible characters, redacts secrets,
// screens and wraps.
func New(o Options) Gate {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	g := &gate{Options: o}
	g.load()
	return g
}

type gate struct {
	Options
	halt   haltFlag
	mu     sync.Mutex
	health string
}

func (g *gate) Decide(ctx context.Context, a Action) (Verdict, error) {
	var v Verdict
	switch {
	case g.halt.get().Halted:
		v = Verdict{Decision: Deny, Reason: haltReason}
	case g.canaryIn(a):
		// Halt first: a failing audit write must not leave the key's user running.
		herr := g.haltForCanary(ctx)
		v = Verdict{Decision: Deny, Reason: haltReason, Detail: "canary"}
		if herr != nil {
			g.Log.Error("halt for canary", "error", herr)
		}
	default:
		v = g.policy(a)
	}
	if v.Decision == Allow {
		return v, nil
	}
	event := EventDeny
	if v.Decision == Ask {
		event = EventAsk
	}
	err := g.Audit.AppendAudit(ctx, AuditEntry{
		Event: event, Principal: a.Principal, Key: a.Key, Kind: string(a.Kind),
		Name: a.Name, Reason: v.Reason, Detail: detail(v),
	})
	if err != nil {
		return Verdict{}, fmt.Errorf("sentry: audit %s: %w", event, err)
	}
	return v, nil
}

// policy is the rule for one action. Later packages add rules here.
func (g *gate) policy(a Action) Verdict {
	if kind := neverShare(a); kind != "" {
		return Verdict{Decision: Deny, Detail: kind, Reason: "this call contains a credential (" + kind +
			"). Remove it and send the call again without it."}
	}
	return Verdict{Decision: Allow}
}

// detail is the audit detail of a verdict, or nil.
func detail(v Verdict) json.RawMessage {
	if v.Detail == "" {
		return nil
	}
	b, _ := json.Marshal(map[string]string{"pattern": v.Detail})
	return b
}

// GuardHealth returns the state observed by the most recent screen call.
func (g *gate) GuardHealth() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.health == "" {
		return "unknown"
	}
	return g.health
}

func (g *gate) guardResult(up bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := "down"
	if up {
		next = "up"
	}
	if g.health == next {
		return
	}
	previous := g.health
	g.health = next
	if g.GuardChanged != nil && previous != "" {
		g.GuardChanged(up)
	}
}
