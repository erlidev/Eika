//go:build live

package live_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

type prefixSource struct{ counter.Source }

func (prefixSource) Boundaries(_ context.Context, prompt string) ([]int, error) {
	return []int{strings.LastIndex(prompt, "<|im_start|>")}, nil
}
func (prefixSource) Text(_ context.Context, text string) (int, error) { return len(text), nil }

func TestStablePromptPrefixChecksEverythingBeforeGenerationHeader(t *testing.T) {
	prev := "old conversation<|im_start|>assistant\n<think>"
	stable, tail, err := stablePromptPrefix(context.Background(), prefixSource{}, prev, "old conversation<|im_start|>assistant\nanswer")
	if err != nil || stable != len("old conversation") || tail != len(prev)-stable {
		t.Fatalf("stable=%d tail=%d err=%v", stable, tail, err)
	}
	if _, _, err := stablePromptPrefix(context.Background(), prefixSource{}, prev, "changed conversation<|im_start|>assistant\nanswer"); err == nil {
		t.Fatal("changed history passed")
	}
	if _, _, err := stablePromptPrefix(context.Background(), prefixSource{}, "old<|im_start|>user", "old<|im_start|>user"); err == nil {
		t.Fatal("non-assistant header passed")
	}
}

func TestQueueProxyForwardsUpstreamHostAndAuthorization(t *testing.T) {
	target, err := url.Parse("https://bench.example")
	if err != nil {
		t.Fatal(err)
	}
	in := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9999/v1/models", nil)
	in.Header.Set("Authorization", "Bearer test-key")
	out := in.Clone(t.Context())
	queueProxyRewrite(target)(&httputil.ProxyRequest{In: in, Out: out})
	if out.URL.Host != target.Host || out.Host != "" || out.Header.Get("Authorization") != "Bearer test-key" || out.URL.Path != "/v1/models" {
		t.Fatalf("upstream request: url=%s host=%q authorization=%q", out.URL, out.Host, out.Header.Get("Authorization"))
	}
}

func TestRecallChecksTheRenderedPromptAndRequiresSearch(t *testing.T) {
	const fact = "violet-hedgehog-7319"
	if err := checkRecallPrompt("prompt with "+fact, "summary says "+fact, fact); err == nil || !strings.Contains(err.Error(), "compaction summary") {
		t.Fatalf("summary leak = %v", err)
	}
	if err := checkRecallPrompt("preamble or reasoning "+fact, "summary without it", fact); err == nil || !strings.Contains(err.Error(), "outside the compaction summary") {
		t.Fatalf("other leak = %v", err)
	}
	if err := checkRecallPrompt("prompt without it", "summary without it", fact); err != nil {
		t.Fatal(err)
	}
	if hasHistorySearch([]string{"history_read"}) || !hasHistorySearch([]string{"history_read", "history_search"}) {
		t.Fatal("recall must use history_search")
	}
}

func TestRecallOutcomeCountsOnlyToolsAfterTheRecallMessage(t *testing.T) {
	const question, fact = "find the phrase", "violet-hedgehog-7319"
	entry := func(seq int64, message provider.Message) store.Entry {
		t.Helper()
		e, err := session.MessageEntry(message)
		if err != nil {
			t.Fatal(err)
		}
		e.Seq, e.RunID = seq, "turn"
		return e
	}
	earlier := provider.AssistantMessage("", []provider.ToolCall{{ID: "before", Name: "history_search"}})
	read := provider.AssistantMessage("", []provider.ToolCall{{ID: "read", Name: "history_read"}})
	entries := []store.Entry{entry(1, earlier), entry(2, provider.UserMessage(question)), entry(3, read), entry(4, provider.AssistantMessage(fact, nil))}
	tools, answered, err := recallOutcome(entries, 0, question, fact)
	if err != nil || !answered || len(tools) != 1 || tools[0] != "history_read" || hasHistorySearch(tools) {
		t.Fatalf("tools=%v answered=%t err=%v", tools, answered, err)
	}
}

func TestCachedPrefixUsesMeasuredGenerationHeader(t *testing.T) {
	if err := checkCachedPrefix(23255, 23251, 5); err != nil {
		t.Fatal(err)
	}
	if err := checkCachedPrefix(23255, 23249, 5); err == nil {
		t.Fatal("accepted a cache count below the measured prefix")
	}
	if err := checkCachedPrefix(4, 0, 5); err == nil {
		t.Fatal("accepted a header larger than the prompt")
	}
}

func TestRecallFactAcceptsCaseAndPunctuation(t *testing.T) {
	for _, text := range []string{"Royal Burgundy beans", "royal-burgundy", "ROYAL, BURGUNDY"} {
		if !containsFact(text, "Royal Burgundy") {
			t.Errorf("did not recognise %q", text)
		}
	}
	if containsFact("a burgundy royal bean", "Royal Burgundy") {
		t.Fatal("accepted the wrong order")
	}
}

func TestSeedInventoryHasUniqueTargetShelf(t *testing.T) {
	inventory := seedInventory()
	rows := strings.Split(strings.TrimSpace(inventory), "\n")
	if len(rows) < 40 {
		t.Fatalf("inventory has %d rows", len(rows))
	}
	if strings.Count(inventory, "shelf K27") != 1 || !strings.Contains(rows[22], "Lot 23:") || !strings.Contains(rows[22], "shelf K27") {
		t.Fatalf("lot 23 does not have its unique shelf: %q", rows[22])
	}
	for _, answer := range []string{"K27", "k-27", "K 27"} {
		if !containsFact(answer, "K27") {
			t.Errorf("did not recognise %q", answer)
		}
	}
	if containsFact("K270", "K27") {
		t.Error("accepted a different shelf")
	}
}

func TestPaddingUsesRetainedHistory(t *testing.T) {
	const keep = 1000
	if got := paddingNeeded(keep, 1300); got != 0 {
		t.Fatalf("already large: %d", got)
	}
	if got := paddingNeeded(keep, 1000); got != 228 {
		t.Fatalf("near threshold: %d", got)
	}
	if got := paddingNeeded(keep, 0); got != 601 {
		t.Fatalf("empty history: %d", got)
	}
}
