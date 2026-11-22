package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/sentry"
)

// coreWebClient checks every request, including redirects and pooled connections.
// Only the configured SearXNG origin uses the local transport.
func coreWebClient(gate func() sentry.Gate, localURL string, fixtures ...string) *http.Client {
	client := fetch.NewClient(fixtures...)
	local, _ := url.Parse(localURL)
	client.Transport = &coreWebTransport{gate: gate, public: client.Transport, local: local, localTransport: http.DefaultTransport}
	return client
}

type coreWebTransport struct {
	gate           func() sentry.Gate
	public         http.RoundTripper
	local          *url.URL
	localTransport http.RoundTripper
}

func (t *coreWebTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.local != nil && t.local.Host != "" && strings.EqualFold(req.URL.Scheme, t.local.Scheme) && strings.EqualFold(req.URL.Host, t.local.Host) {
		return t.localTransport.RoundTrip(req)
	}
	host := req.URL.Hostname()
	v, err := t.gate().Decide(req.Context(), sentry.Action{Kind: sentry.KindEgress, Name: host, Destination: host})
	if err != nil {
		return nil, fmt.Errorf("check egress for %s: %w", host, err)
	}
	if v.Decision != sentry.Allow {
		return nil, fmt.Errorf("egress to %s denied: %s", host, v.Reason)
	}
	return t.public.RoundTrip(req)
}
