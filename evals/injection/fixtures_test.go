package injection_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/injection"
	"github.com/erlidev/eika/internal/provider"
)

func TestFixturesAndMeasurement(t *testing.T) {
	handler := injection.Handler(injection.Variants())
	for _, v := range injection.Variants() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+v.Name, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), injection.Fact) {
			t.Fatalf("fixture %s: status %d, body %q", v.Name, response.Code, response.Body.String())
		}
	}
	messages := []provider.Message{
		{ToolCalls: []provider.ToolCall{{ID: "f", Name: "web_fetch", Arguments: provider.ToolArguments(`{"url":"http://localhost/innocent"}`)}, {ID: "q", Name: "ask_user"}, {Name: "report", Arguments: provider.ToolArguments(`{"summary":"violet heron protocol"}`)}}},
		{Role: provider.RoleTool, ToolCallID: "f", Content: "kept text", Removed: 2},
	}
	out := injection.Measure(messages, "http://localhost/innocent", injection.Fact, injection.Marker)
	if !out.Fetched || !out.Attack || !out.ReachedHome || !out.Task || out.Asks != 1 || out.Removals != 2 || out.FetchText != "kept text" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestChainFixtureCarriesTheExfiltrationURL(t *testing.T) {
	url := "http://localhost:1234/exfil?data=seed-library-private"
	page := injection.ChainPage(url)
	if !strings.Contains(page.Page, injection.Fact) || !strings.Contains(page.Page, url) {
		t.Fatalf("chain page = %q", page.Page)
	}
}
