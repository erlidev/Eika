package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/search/web"
	"github.com/erlidev/eika/internal/search/wikipedia"
	"github.com/erlidev/eika/internal/sentry"
)

type coreAudit struct {
	rows  []sentry.AuditEntry
	state sentry.HaltState
}

func (a *coreAudit) AppendAudit(_ context.Context, row sentry.AuditEntry) error {
	a.rows = append(a.rows, row)
	return nil
}
func (a *coreAudit) Halt(context.Context) (sentry.HaltState, error) { return a.state, nil }
func (a *coreAudit) SetHalt(_ context.Context, state sentry.HaltState, _ sentry.AuditEntry) error {
	a.state = state
	return nil
}

func TestCoreWebEgress(t *testing.T) {
	a := &coreAudit{}
	g := sentry.New(sentry.Options{Audit: a, Halts: a, BlockedHost: func(_ context.Context, host string) (bool, error) { return host == "blocked.example", nil }})
	gate := func() sentry.Gate { return g }
	client := coreWebClient(gate, "")
	reader := fetch.New(fetch.Config{Client: client})
	out, err := reader.Read(t.Context(), fetch.Request{URL: "https://blocked.example/page"}, nil)
	if err != nil || !out.IsError || !strings.Contains(out.Text, "host is blocked") {
		t.Fatalf("fetch: %+v, %v", out, err)
	}
	_, err = wikipedia.New(client, "https://blocked.example").Search(t.Context(), search.Query{Text: "test", Limit: 1})
	if err == nil || !strings.Contains(err.Error(), "host is blocked") {
		t.Fatalf("search: %v", err)
	}
	if len(a.rows) != 2 {
		t.Fatalf("audit rows: %d", len(a.rows))
	}
	if _, _, err = g.Halt(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	_, err = wikipedia.New(client, "https://allowed.example").Search(t.Context(), search.Query{Text: "test", Limit: 1})
	if err == nil || !strings.Contains(err.Error(), "halted") || len(a.rows) != 3 {
		t.Fatalf("halt: %v, rows: %d", err, len(a.rows))
	}
	localClient := coreWebClient(gate, "http://searxng:8080")
	localClient.Transport.(*coreWebTransport).localTransport = localResponse{}
	local := web.NewSearxNG(localClient, "http://searxng:8080")
	if _, err := local.Search(t.Context(), search.Query{Text: "test", Limit: 1}); err != nil {
		t.Fatalf("local searxng: %v", err)
	}
	if _, err := local.Search(t.Context(), search.Query{Text: "redirect", Limit: 1}); err == nil || !strings.Contains(err.Error(), "halted") {
		t.Fatalf("redirect: %v", err)
	}
	if _, _, err := g.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Search(t.Context(), search.Query{Text: "redirect", Limit: 1}); err == nil || !strings.Contains(err.Error(), "host is blocked") {
		t.Fatalf("blocked redirect: %v", err)
	}
}

type localResponse struct{}

func (localResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Query().Get("q") == "redirect" {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://blocked.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"results":[]}`)), Request: r}, nil
}
