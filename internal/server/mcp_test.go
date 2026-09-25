package server_test

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/server"
)

func TestTheClientMetadataDocument(t *testing.T) {
	get := func(publicURL string) *httptest.ResponseRecorder {
		t.Helper()
		cfg := testConfig()
		cfg.PublicURL = publicURL
		pool := server.NewMCP(cfg, nil, nil, nil, nil, testLogger())
		t.Cleanup(pool.Close)
		s := server.New(cfg, testLogger(), server.Deps{MCP: pool}, server.Options{})
		rec := httptest.NewRecorder()
		// No token: the authorization server that fetches it has none.
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/oauth/client-metadata.json", nil))
		return rec
	}

	rec := get("https://eika.example.com")
	var doc struct {
		ClientID     string   `json:"client_id"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		t.Fatalf("metadata = %d %s", rec.Code, rec.Body.String())
	}
	if doc.ClientID != "https://eika.example.com/oauth/client-metadata.json" || !slices.Equal(doc.RedirectURIs, []string{"https://eika.example.com/mcp/callback"}) {
		t.Errorf("document = %+v", doc)
	}
	for _, publicURL := range []string{"", "http://10.0.0.5:8080"} {
		if rec := get(publicURL); rec.Code != 404 {
			t.Errorf("public_url %q = %d, want 404: only an https address can be fetched", publicURL, rec.Code)
		}
	}
}
