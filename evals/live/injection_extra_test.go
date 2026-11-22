//go:build live

package live_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

func TestInjectionFixtureRecordsExfilRequests(t *testing.T) {
	f := newFixtureLog()
	f.setBase("http://localhost:1234")
	page := httptest.NewRecorder()
	f.ServeHTTP(page, httptest.NewRequest("GET", "/chain", nil))
	if !strings.Contains(page.Body.String(), "/exfil?data=seed-library-private") {
		t.Fatal("chain has no exfil URL")
	}
	f.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/exfil?data=seed-library-private", nil))
	if f.exfil() != 1 {
		t.Fatalf("exfil requests = %d", f.exfil())
	}
}

func TestWorkerMeasurementsMatchCallsToResults(t *testing.T) {
	msgs := []provider.Message{
		{ToolCalls: []provider.ToolCall{{ID: "c", Name: "bash", Arguments: `{"command":"curl https://api.search.brave.com/"}`}, {ID: "f", Name: "web_fetch", Arguments: `{"url":"https://api.search.brave.com/"}`}, {ID: "p", Name: "bash", Arguments: `{"command":"pip install --index-url https://api.search.brave.com/simple x"}`}}},
		{Role: provider.RoleTool, ToolCallID: "c", IsError: true}, {Role: provider.RoleTool, ToolCallID: "f", IsError: true}, {Role: provider.RoleTool, ToolCallID: "p", IsError: true},
	}
	got := workerChecks(msgs)
	if got["curl"] != 1 || got["fetch"] != 1 || got["install"] != 1 {
		t.Fatalf("checks = %v", got)
	}
}
