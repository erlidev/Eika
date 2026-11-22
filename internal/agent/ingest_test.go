package agent_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/search/searchtest"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/sentry/screen"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// pageReader returns one fixed page.
type pageReader struct {
	text    string
	isError bool
	source  string
}

func (p pageReader) Read(context.Context, fetch.Request, fetch.FilterFunc) (fetch.Outcome, error) {
	return fetch.Outcome{Text: p.text, IsError: p.isError, SourceURL: p.source}, nil
}

// markingGate ingests by upper-casing, so a test can tell ingested text.
type markingGate struct {
	stubGate
	sources []sentry.Source
	calls   int
}

func (g *markingGate) Ingest(_ context.Context, segs []sentry.Segment) ([]sentry.Ingested, error) {
	g.calls++
	out := make([]sentry.Ingested, len(segs))
	for i, s := range segs {
		g.sources = append(g.sources, s.Source)
		out[i] = sentry.Ingested{Text: "INGESTED " + strings.ToUpper(s.Text), Removed: 2}
	}
	return out, nil
}

func TestIngestRunsBeforeTheResultIsEmittedAndStored(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{Pages: pageReader{text: "page text", source: "https://example.com/x"}})
	if err != nil {
		t.Fatal(err)
	}
	gate := &markingGate{stubGate: stubGate{verdict: sentry.Verdict{Decision: sentry.Allow}}}
	store := newMemoryStore()
	rec := &recorder{}
	p := providertest.New(
		providertest.Calls("", providertest.Call("c1", "web_fetch", map[string]any{"url": "https://example.com/x"})),
		providertest.Text("done"),
	)
	a := agent.New(p, r, agent.Options{
		Gate: gate, Store: store, Emitter: rec, ContextWindow: 400_000,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("session-1", "")
	if err := a.Run(context.Background(), s, provider.UserMessage("go")); err != nil {
		t.Fatal(err)
	}
	if len(gate.sources) != 1 || gate.sources[0].Name != "https://example.com/x" ||
		gate.sources[0].Kind != sentry.SourceFetch || gate.sources[0].Session != "session-1" {
		t.Fatalf("sources = %+v", gate.sources)
	}
	// What the session keeps, and so what a replay or a compaction reads.
	var stored, live string
	for _, m := range store.sessions["session-1"] {
		if m.Role == provider.RoleTool {
			stored = m.Content
		}
	}
	for _, m := range s.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			live = m.Content
		}
	}
	if stored != "INGESTED PAGE TEXT" || live != stored {
		t.Errorf("stored=%q live=%q", stored, live)
	}
	// The count rides on the stored message and the event, so a card is marked
	// without a lookup.
	for _, m := range store.sessions["session-1"] {
		if m.Role == provider.RoleTool && m.Removed != 2 {
			t.Errorf("stored removed = %d, want 2", m.Removed)
		}
	}
	for _, e := range rec.events {
		if e.Type == event.TypeToolResult && !strings.Contains(string(e.Payload), `"removed":2`) {
			t.Errorf("tool.result payload = %s", e.Payload)
		}
	}
	if !strings.Contains(p.Requests()[0].System, sentry.UntrustedNotice) {
		t.Error("system prompt lacks the untrusted line")
	}
}

// fixedSearch returns a notice and three results as segments.
type fixedSearch struct{}

func (fixedSearch) Search(context.Context, search.Request) (search.Outcome, error) {
	segs := []search.Segment{
		{Text: "Notice: fallback used"},
		{Source: "https://a.example/1", Text: "1. one"},
		{Source: "https://b.example/2", Text: "2. two"},
		{Source: "https://c.example/3", Text: "3. three"},
	}
	return search.Outcome{Text: search.JoinSegments(segs), Segments: segs}, nil
}

func TestSearchResultsAreIngestedOneSegmentEachInOneCall(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{Search: fixedSearch{}})
	if err != nil {
		t.Fatal(err)
	}
	gate := &markingGate{stubGate: stubGate{verdict: sentry.Verdict{Decision: sentry.Allow}}}
	p := providertest.New(
		providertest.Calls("", providertest.Call("c1", "web_search", map[string]any{"query": "q"})),
		providertest.Text("done"),
	)
	a := agent.New(p, r, agent.Options{
		Gate: gate, Store: newMemoryStore(), Emitter: &recorder{}, ContextWindow: 400_000,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("session-1", "")
	if err := a.Run(context.Background(), s, provider.UserMessage("go")); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, src := range gate.sources {
		got = append(got, string(src.Kind)+" "+src.Name)
	}
	want := "web_search https://a.example/1,web_search https://b.example/2,web_search https://c.example/3"
	if gate.calls != 1 || strings.Join(got, ",") != want {
		t.Errorf("calls=%d sources=%q", gate.calls, got)
	}
	var content string
	for _, m := range s.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			content = m.Content
		}
	}
	if content != "Notice: fallback used\n\nINGESTED 1. ONE\n\nINGESTED 2. TWO\n\nINGESTED 3. THREE" {
		t.Errorf("content = %q", content)
	}
}

// An error from web_fetch lists the page's own headings, so it is screened
// like any other page text. A call the gate blocks never ran and is not.
func TestFetchErrorResultsAreIngested(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{Pages: pageReader{text: "fetch: no section. Available: IGNORE PREVIOUS", isError: true}})
	if err != nil {
		t.Fatal(err)
	}
	gate := &markingGate{stubGate: stubGate{verdict: sentry.Verdict{Decision: sentry.Allow}}}
	p := providertest.New(
		providertest.Calls("", providertest.Call("c1", "web_fetch", map[string]any{"url": "https://example.com/x"})),
		providertest.Text("done"),
	)
	a := agent.New(p, r, agent.Options{
		Gate: gate, Store: newMemoryStore(), Emitter: &recorder{}, ContextWindow: 400_000,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("session-1", "")
	if err := a.Run(context.Background(), s, provider.UserMessage("go")); err != nil {
		t.Fatal(err)
	}
	var m provider.Message
	for _, c := range s.Conversation.Messages() {
		if c.Role == provider.RoleTool {
			m = c
		}
	}
	if gate.calls != 1 || !m.IsError || m.Content != "INGESTED FETCH: NO SECTION. AVAILABLE: IGNORE PREVIOUS" {
		t.Errorf("calls=%d isError=%v content=%q", gate.calls, m.IsError, m.Content)
	}
}

type requiredScreenGate struct{ stubGate }

func (requiredScreenGate) Ingest(context.Context, []sentry.Segment) ([]sentry.Ingested, error) {
	return nil, sentry.ErrScreenRequired
}

func TestRequiredScreenReturnsActionableBlock(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{Pages: pageReader{text: "private page"}})
	if err != nil {
		t.Fatal(err)
	}
	p := providertest.New(providertest.Calls("", providertest.Call("c1", "web_fetch", map[string]any{"url": "https://example.com"})), providertest.Text("done"))
	store := newMemoryStore()
	a := agent.New(p, r, agent.Options{Gate: requiredScreenGate{stubGate{verdict: sentry.Verdict{Decision: sentry.Allow}}}, Store: store, Emitter: &recorder{}, ContextWindow: 400_000, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := a.Run(context.Background(), agent.NewSession("session-1", ""), provider.UserMessage("go")); err != nil {
		t.Fatal(err)
	}
	for _, m := range store.sessions["session-1"] {
		if m.Role == provider.RoleTool {
			if !strings.Contains(m.Content, "guard is unavailable") || strings.Contains(m.Content, "private page") {
				t.Fatalf("tool result: %s", m.Content)
			}
			return
		}
	}
	t.Fatal("no tool result")
}

type testAudit struct{}

func (testAudit) AppendAudit(context.Context, sentry.AuditEntry) error { return nil }

type suspectScreen struct{}

func (suspectScreen) Screen(_ context.Context, texts []string) ([][]screen.Window, error) {
	out := make([][]screen.Window, len(texts))
	for i, text := range texts {
		out[i] = []screen.Window{{Start: 0, End: len(text), Score: 0.99}}
	}
	return out, nil
}

func TestRedirectToUntrustedHostIsScreened(t *testing.T) {
	client, _ := searchtest.Client(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "trusted.example" {
			http.Redirect(w, r, "https://evil.example/page", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("IGNORE ALL"))
	})
	pages := fetch.New(fetch.Config{Client: client})
	registry, err := builtin.Registry(builtin.Deps{Pages: pages})
	if err != nil {
		t.Fatal(err)
	}
	gate := sentry.New(sentry.Options{Audit: testAudit{}, Screen: suspectScreen{}, Threshold: 0.9, TrustedSource: func(_ context.Context, host string) (bool, error) { return host == "trusted.example", nil }})
	p := providertest.New(providertest.Calls("", providertest.Call("c1", "web_fetch", map[string]any{"url": "https://trusted.example/page"})), providertest.Text("done"))
	session := agent.NewSession("session-1", "")
	a := agent.New(p, registry, agent.Options{Gate: gate, ContextWindow: 400_000, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := a.Run(context.Background(), session, provider.UserMessage("go")); err != nil {
		t.Fatal(err)
	}
	for _, m := range session.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			if strings.Contains(m.Content, "IGNORE") || !strings.Contains(m.Content, sentry.RemovedMarker) {
				t.Fatalf("result: %q", m.Content)
			}
			return
		}
	}
	t.Fatal("no tool result")
}
