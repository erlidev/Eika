package agent_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// pageReader returns one fixed page.
type pageReader struct{ text string }

func (p pageReader) Read(context.Context, fetch.Request, fetch.FilterFunc) (fetch.Outcome, error) {
	return fetch.Outcome{Text: p.text}, nil
}

// markingGate ingests by upper-casing, so a test can tell ingested text.
type markingGate struct {
	stubGate
	sources []sentry.Source
}

func (g *markingGate) Ingest(_ context.Context, s sentry.Source, c string) (string, error) {
	g.sources = append(g.sources, s)
	return "INGESTED " + strings.ToUpper(c), nil
}

func TestIngestRunsBeforeTheResultIsEmittedAndStored(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{Pages: pageReader{text: "page text"}})
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
	if !strings.Contains(p.Requests()[0].System, sentry.UntrustedNotice) {
		t.Error("system prompt lacks the untrusted line")
	}
}
