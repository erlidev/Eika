package sentry_test

import (
	"context"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/sentry"
)

type memHalt struct {
	state sentry.HaltState
	audit []sentry.AuditEntry
}

func (m *memHalt) Halt(context.Context) (sentry.HaltState, error) { return m.state, nil }
func (m *memHalt) SetHalt(_ context.Context, st sentry.HaltState, a sentry.AuditEntry) error {
	m.state = st
	m.audit = append(m.audit, a)
	return nil
}
func (m *memHalt) AppendAudit(_ context.Context, a sentry.AuditEntry) error {
	m.audit = append(m.audit, a)
	return nil
}

func TestHaltDeniesEveryActionUntilResumed(t *testing.T) {
	ctx := t.Context()
	m := &memHalt{}
	g := sentry.New(sentry.Options{Audit: m, Halts: m})
	act := sentry.Action{Kind: sentry.KindTool, Name: "file_read"}
	if v, _ := g.Decide(ctx, act); v.Decision != sentry.Allow {
		t.Fatalf("before halt: %+v", v)
	}
	if _, changed, err := g.Halt(ctx, "test"); err != nil || !changed {
		t.Fatalf("halt: %v %v", changed, err)
	}
	if _, changed, _ := g.Halt(ctx, "again"); changed {
		t.Error("halting a halted gate changed it")
	}
	v, err := g.Decide(ctx, act)
	if err != nil || v.Decision != sentry.Deny || v.Reason == "" {
		t.Fatalf("while halted: %+v %v", v, err)
	}
	// A gate built after a restart reads the stored state.
	if v, _ := sentry.New(sentry.Options{Audit: m, Halts: m}).Decide(ctx, act); v.Decision != sentry.Deny {
		t.Errorf("after restart: %+v", v)
	}
	if _, changed, err := g.Resume(ctx); err != nil || !changed {
		t.Fatalf("resume: %v %v", changed, err)
	}
	if v, _ := g.Decide(ctx, act); v.Decision != sentry.Allow {
		t.Errorf("after resume: %+v", v)
	}
	var events []string
	for _, a := range m.audit {
		events = append(events, a.Event)
	}
	if want := []string{"halt", "deny", "deny", "resume"}; !slices.Equal(events, want) {
		t.Errorf("audit events = %v, want %v", events, want)
	}
}
