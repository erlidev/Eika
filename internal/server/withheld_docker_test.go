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
