//go:build docker

package server_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/sentry"
)

func TestWithheldListIsNewestFirstAndPaged(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	if rec := requestWith(t, a.Server, http.MethodGet, "/api/withheld", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status %d", rec.Code)
	}
	base := time.Now().UTC().Add(-time.Hour)
	var want []string // newest first
	for i := range 5 {
		w := sentry.Withheld{
			ID: fmt.Sprintf("w%d", i), At: base.Add(time.Duration(i) * time.Minute), Text: fmt.Sprintf("text %d", i), Score: 0.95,
			Source: sentry.Source{Kind: sentry.SourceFetch, Name: "https://example.com", Session: "s1", Key: fmt.Sprintf("s1:c%d", i)},
		}
		if i == 3 { // the same instant as row 2: the id breaks the tie
			w.At = base.Add(2 * time.Minute)
		}
		if err := a.store.AppendWithheld(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	want = []string{"w4", "w3", "w2", "w1", "w0"}
	type page struct {
		Withheld []struct {
			ID      string `json:"id"`
			Text    string `json:"text"`
			CallKey string `json:"call_key"`
		} `json:"withheld"`
		Next string `json:"next_before"`
	}
	var got []string
	query := "?limit=2"
	for range 5 {
		p := decodeBody[page](t, request(t, a.Server, http.MethodGet, "/api/withheld"+query, nil), http.StatusOK)
		for _, w := range p.Withheld {
			got = append(got, w.ID)
		}
		if p.Next == "" {
			break
		}
		query = "?limit=2&before=" + p.Next
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	one := decodeBody[struct {
		Text    string `json:"text"`
		CallKey string `json:"call_key"`
	}](t, request(t, a.Server, http.MethodGet, "/api/withheld/w2", nil), http.StatusOK)
	if one.Text != "text 2" || one.CallKey != "s1:c2" {
		t.Fatalf("one: %+v", one)
	}
	for path, code := range map[string]int{
		"/api/withheld/nope":        http.StatusNotFound,
		"/api/withheld?before=nope": http.StatusBadRequest,
		"/api/withheld?limit=0":     http.StatusBadRequest,
	} {
		if rec := request(t, a.Server, http.MethodGet, path, nil); rec.Code != code {
			t.Errorf("%s: status %d, want %d", path, rec.Code, code)
		}
	}
}

func TestTrustedSourcesAPI(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	if rec := request(t, a.Server, http.MethodPost, "/api/trusted-sources", map[string]string{"host": "OTHER.example."}); rec.Code != http.StatusNoContent {
		t.Fatalf("add: %d", rec.Code)
	}
	if rec := request(t, a.Server, http.MethodPost, "/api/trusted-sources", map[string]string{"host": "https://example.com"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid host: %d", rec.Code)
	}
	if err := a.store.AppendWithheld(ctx, sentry.Withheld{ID: "trust-row", Source: sentry.Source{Kind: sentry.SourceFetch, Name: "https://Example.com/page"}, Text: "text", Score: 0.99}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/withheld/trust-row/trust", "/api/withheld/trust-row/trust"} {
		if rec := request(t, a.Server, http.MethodPost, path, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("trust: %d", rec.Code)
		}
	}
	got := decodeBody[struct {
		Hosts []string `json:"hosts"`
	}](t, request(t, a.Server, http.MethodGet, "/api/trusted-sources", nil), http.StatusOK)
	if !reflect.DeepEqual(got.Hosts, []string{"example.com", "other.example"}) {
		t.Fatalf("hosts: %v", got.Hosts)
	}
	if rec := request(t, a.Server, http.MethodDelete, "/api/trusted-sources/example.com", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	trusted, err := a.store.IsTrustedSource(ctx, "example.com")
	if err != nil || trusted {
		t.Fatalf("trusted: %v, %v", trusted, err)
	}
}

func TestBlockedHostsAPI(t *testing.T) {
	a := newAPI(t)
	if rec := request(t, a.Server, http.MethodPost, "/api/blocked-hosts", map[string]string{"host": "OTHER.example."}); rec.Code != http.StatusNoContent {
		t.Fatalf("add: %d", rec.Code)
	}
	if rec := request(t, a.Server, http.MethodPost, "/api/blocked-hosts", map[string]string{"host": "https://example.com"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid: %d", rec.Code)
	}
	rows := decodeBody[struct {
		Hosts []string `json:"hosts"`
	}](t, request(t, a.Server, http.MethodGet, "/api/blocked-hosts", nil), http.StatusOK)
	if len(rows.Hosts) != 1 || rows.Hosts[0] != "other.example" {
		t.Fatalf("hosts: %v", rows.Hosts)
	}
	if rec := request(t, a.Server, http.MethodDelete, "/api/blocked-hosts/other.example", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if blocked, err := a.store.IsBlockedHost(t.Context(), "other.example"); err != nil || blocked {
		t.Fatalf("after delete: %v %v", blocked, err)
	}
}
