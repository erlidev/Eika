package live_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/evals/live"
)

func ptr[T any](v T) *T { return &v }

// intOf is a measured count, or -1 for one nothing measured.
func intOf(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func TestSummariseTotalsWhatEveryCallMeasured(t *testing.T) {
	tests := []struct {
		name      string
		calls     []live.Call
		cached    *int
		rate      *float64
		prefill   *float64
		drift     *int
		promptSum *int
	}{
		{
			name: "all measured",
			calls: []live.Call{
				{PromptTokens: ptr(100), CachedTokens: ptr(0), PrefillSeconds: ptr(1.0), Drift: ptr(2)},
				{PromptTokens: ptr(100), CachedTokens: ptr(50), PrefillSeconds: ptr(0.5), Drift: ptr(-1)},
			},
			cached: ptr(50), rate: ptr(0.25), prefill: ptr(1.5), drift: ptr(1), promptSum: ptr(200),
		},
		{
			name: "one call without a cache count",
			calls: []live.Call{
				{PromptTokens: ptr(100), CachedTokens: ptr(0), PrefillSeconds: ptr(1.0)},
				{PromptTokens: ptr(100), PrefillSeconds: ptr(0.5)},
			},
			prefill: ptr(1.5), promptSum: ptr(200),
		},
		{
			name:      "nothing measured",
			calls:     []live.Call{{PromptTokens: ptr(7)}},
			promptSum: ptr(7),
		},
		{name: "no calls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := live.Summarise(tt.calls)
			if !equal(s.PromptTokens, tt.promptSum) || s.Calls != len(tt.calls) {
				t.Errorf("calls %d, prompt tokens %v", s.Calls, s.PromptTokens)
			}
			if !equal(s.CachedTokens, tt.cached) || !equal(s.CacheHitRate, tt.rate) ||
				!equal(s.PrefillSeconds, tt.prefill) || !equal(s.Drift, tt.drift) {
				t.Errorf("summary = %+v", s)
			}
		})
	}
}

func equal[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestReportWritesJSONWithTheServerAndAbsentDrift(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "results", "nested")
	started := time.Date(2026, 10, 31, 12, 30, 5, 0, time.UTC)
	report := live.NewReport(started, live.ServerInfo{Build: "b11942-338fce651", Model: "qwen3.8-27b", ModelRevision: "4ca7"})
	call := live.Call{Label: "turn 1", PromptTokens: ptr(10)}
	report.Add(live.Result{Name: "s", Passed: true, Calls: []live.Call{call}, Summary: live.Summarise([]live.Call{call})})

	file, err := report.Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "20261031T123005Z-b11942-338fce651.json"); file != want {
		t.Errorf("file = %s, want %s", file, want)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Server    live.ServerInfo `json:"server"`
		Scenarios []struct {
			Name    string                       `json:"name"`
			Summary map[string]json.RawMessage   `json:"summary"`
			Calls   []map[string]json.RawMessage `json:"calls"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("report is not JSON: %v", err)
	}
	if got.Server.Build != "b11942-338fce651" || got.Server.ModelRevision != "4ca7" {
		t.Errorf("server = %+v", got.Server)
	}
	if len(got.Scenarios) != 1 {
		t.Fatalf("%d scenarios", len(got.Scenarios))
	}
	s := got.Scenarios[0]
	// Drift is present and null, never 0, so a reader sees it was not measured.
	for name, raw := range map[string]json.RawMessage{"summary": s.Summary["drift"], "call": s.Calls[0]["drift"]} {
		if strings.TrimSpace(string(raw)) != "null" {
			t.Errorf("%s drift = %s, want null", name, raw)
		}
	}
	if strings.TrimSpace(string(s.Calls[0]["cache_hit_rate"])) != "null" {
		t.Errorf("an unmeasured hit rate = %s, want null", s.Calls[0]["cache_hit_rate"])
	}
}

func TestReportWithoutScenariosListsNone(t *testing.T) {
	file, err := live.NewReport(time.Now(), live.ServerInfo{}).Write(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if !strings.Contains(string(raw), `"scenarios": []`) {
		t.Errorf("report = %s, want an empty list, not null", raw)
	}
}
