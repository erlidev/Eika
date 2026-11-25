package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/erlidev/eika/internal/sentry/scan"
)

// MemoryChecker is the write-time check of docs/design/memory.md, section 6.
// Gate.Ingest cannot serve it: it redacts, removes and wraps, and a memory
// candidate must be kept intact or not stored. The gate returned by New
// implements it; callers (the memory tools and Reconcile) assert for it.
type MemoryChecker interface {
	CheckMemory(ctx context.Context, c MemoryCandidate) (MemoryVerdict, error)
}

// MemoryCandidate is a memory about to be written. Both Description and Body
// reach the model's context, so both are checked. Trusted web hosts are not
// consulted: they never bypass this check.
type MemoryCandidate struct {
	Principal   Principal
	Key         string // the tool call's key, when there is one
	Name        string
	Description string
	Body        string
}

// MemoryDecision is what the caller does with a candidate.
type MemoryDecision string

// The outcomes of a memory check.
const (
	MemoryActive  MemoryDecision = "active"  // write with state active
	MemoryPending MemoryDecision = "pending" // write intact with state pending
	MemoryDeny    MemoryDecision = "deny"    // credential: write nothing, tell the model
	MemoryReject  MemoryDecision = "reject"  // guard down and screen_required: write nothing
)

// EventMemoryPending is the audit event of a pending memory. A deny or reject reuses EventDeny.
const EventMemoryPending = "memory_pending"

// MemorySpan is a screened window at or over the threshold. Field is
// "description" or "body"; Start and End are byte offsets into the screened
// text (the field with invisible characters removed), and Text is that slice.
type MemorySpan struct {
	Field string  `json:"field"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// MemoryVerdict is CheckMemory's answer. Spans and Unscanned are what the
// Inbox shows for a pending memory; the caller stores them with the candidate.
type MemoryVerdict struct {
	Decision MemoryDecision
	// Reason is one plain sentence for the model when nothing is written.
	Reason string
	// Pattern is the credential kind for a deny, never its value.
	Pattern string
	// Spans are the windows at or over the threshold (pending by a hit).
	Spans []MemorySpan
	// Unscanned is true when the guard did not answer (pending, or reject).
	Unscanned bool
	// MaxScore is the highest score of Spans.
	MaxScore float64
}

// CheckMemory decides the state of a memory candidate, in the order of the
// design: a credential pattern in the description or body denies; otherwise
// both are screened, and a score at or over the threshold, or a guard that did
// not answer, makes it pending, or a reject when screening is required. An
// error means the check could not run and nothing may be written. Non-allow
// outcomes are audited without text or secret values.
func (g *gate) CheckMemory(ctx context.Context, c MemoryCandidate) (MemoryVerdict, error) {
	if err := ctx.Err(); err != nil {
		return MemoryVerdict{}, fmt.Errorf("sentry: check memory: %w", err)
	}
	fields := []string{"description", "body"}
	texts := []string{
		scan.StripInvisible(strings.ToValidUTF8(c.Description, "�")),
		scan.StripInvisible(strings.ToValidUTF8(c.Body, "�")),
	}
	for i, t := range texts {
		if kind := scan.Find(t); kind != "" {
			v := MemoryVerdict{Decision: MemoryDeny, Pattern: kind,
				Reason: "this memory contains a credential (" + kind + ") in its " + fields[i] +
					". Remove it and save the memory again without it."}
			return v, g.auditMemory(ctx, c, EventDeny, v)
		}
	}
	threshold, required := g.Threshold, false
	if g.ScreenSettings != nil {
		var err error
		if threshold, required, err = g.ScreenSettings(ctx); err != nil {
			return MemoryVerdict{}, fmt.Errorf("sentry: read screen settings: %w", err)
		}
	}
	if g.MemoryThreshold != nil {
		var err error
		if threshold, err = g.MemoryThreshold(ctx); err != nil {
			return MemoryVerdict{}, fmt.Errorf("sentry: read memory threshold: %w", err)
		}
	}
	src := Source{Kind: SourceKind("memory"), Session: c.Principal.Session, Run: c.Principal.Run, Key: c.Key, Name: c.Name}
	windows, unscanned := g.score(ctx, []Segment{{Source: src}}, texts)
	if err := ctx.Err(); err != nil {
		return MemoryVerdict{}, fmt.Errorf("sentry: check memory: %w", err)
	}
	if unscanned {
		v := MemoryVerdict{Decision: MemoryPending, Unscanned: true}
		event := EventMemoryPending
		if required {
			v = MemoryVerdict{Decision: MemoryReject, Unscanned: true,
				Reason: "memory cannot be saved right now because screening is required and unavailable. Try again later."}
			event = EventDeny
		}
		return v, g.auditMemory(ctx, c, event, v)
	}
	var v MemoryVerdict
	for i, ws := range windows {
		for _, w := range ws {
			if w.Score < threshold {
				continue
			}
			start, end := runeStart(texts[i], max(w.Start, 0)), runeStart(texts[i], min(w.End, len(texts[i])))
			if start >= end {
				continue
			}
			v.Spans = append(v.Spans, MemorySpan{Field: fields[i], Start: start, End: end, Score: w.Score, Text: texts[i][start:end]})
			v.MaxScore = max(v.MaxScore, w.Score)
		}
	}
	if len(v.Spans) == 0 {
		return MemoryVerdict{Decision: MemoryActive}, nil
	}
	sort.SliceStable(v.Spans, func(a, b int) bool {
		if v.Spans[a].Field != v.Spans[b].Field {
			return v.Spans[a].Field == "description"
		}
		return v.Spans[a].Start < v.Spans[b].Start
	})
	v.Decision = MemoryPending
	return v, g.auditMemory(ctx, c, EventMemoryPending, v)
}

func (g *gate) auditMemory(ctx context.Context, c MemoryCandidate, event string, v MemoryVerdict) error {
	d := map[string]any{"decision": string(v.Decision)}
	if v.Pattern != "" {
		d["pattern"] = v.Pattern
	}
	if v.Unscanned {
		d["unscanned"] = true
	}
	if len(v.Spans) > 0 {
		d["spans"], d["score"] = len(v.Spans), v.MaxScore
	}
	detail, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("sentry: encode memory decision: %w", err)
	}
	err = g.Audit.AppendAudit(ctx, AuditEntry{
		Event: event, Principal: c.Principal, Key: c.Key, Kind: "memory", Name: c.Name,
		Reason: v.Reason, Detail: detail,
	})
	if err != nil {
		return fmt.Errorf("sentry: audit memory %s: %w", v.Decision, err)
	}
	return nil
}
