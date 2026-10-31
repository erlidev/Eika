package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/evals/live"
	"github.com/erlidev/eika/internal/provider"
)

// slotServer stands in for a llama.cpp server with two slots, one word a token.
// A slot holds the words of the last prompt it read, and a request is placed as
// the server places it: on the slot id_slot names, if the server reads the
// field, and otherwise on the slot whose cache shares the most with the prompt,
// else the one used longest ago. With clearIdle it does what llama.cpp does
// with a unified cache and --cache-idle-slots: when a request starts, every
// other idle slot is emptied and its words go to a host cache, which only a
// request that names no slot is restored from.
type slotServer struct {
	*httptest.Server
	honour    bool
	clearIdle bool
	pause     time.Duration

	mu    sync.Mutex
	slots [2]slotState
	host  [][]string
	tick  int
	named []int
}

type slotState struct {
	words []string
	used  int
	busy  bool
}

func newSlotServer(t *testing.T, honour, clearIdle bool) *slotServer {
	t.Helper()
	bench := benchServer(t)
	s := &slotServer{honour: honour, clearIdle: clearIdle, pause: 40 * time.Millisecond}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]string{"prompt": strings.Join(promptWords(body), " ")})
		case "/tokenize":
			var asked struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&asked)
			_, _ = fmt.Fprintf(w, `{"tokens":[%s]}`, strings.TrimSuffix(strings.Repeat("7,", len(strings.Fields(asked.Content))), ","))
		case "/slots":
			s.mu.Lock()
			out := make([]map[string]any, len(s.slots))
			for i, sl := range s.slots {
				out[i] = map[string]any{"id": i, "is_processing": sl.busy}
			}
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(out)
		case "/v1/chat/completions":
			s.complete(w, r)
		default:
			bench.Config.Handler.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// promptWords renders a chat body as the words of its prompt.
func promptWords(body map[string]any) []string {
	var words []string
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if text, ok := msg["content"].(string); ok {
			words = append(words, strings.Fields(text)...)
		}
	}
	return words
}

// common is the length of the longest common prefix of a and b.
func common(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func (s *slotServer) complete(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	words := promptWords(body)

	s.mu.Lock()
	s.tick++
	pinned := -1
	if id, ok := body["id_slot"].(float64); ok && s.honour {
		pinned = int(id) % len(s.slots)
	}
	if id, ok := body["id_slot"].(float64); ok {
		s.named = append(s.named, int(id))
	} else {
		s.named = append(s.named, -1)
	}
	chosen := pinned
	if chosen < 0 {
		best := 0.0
		for i, sl := range s.slots {
			if sl.busy || len(sl.words) == 0 {
				continue
			}
			if sim := float64(common(sl.words, words)) / float64(len(words)); sim > best && sim > 0.1 {
				best, chosen = sim, i
			}
		}
	}
	if chosen < 0 {
		for i, sl := range s.slots {
			if !sl.busy && (chosen < 0 || sl.used < s.slots[chosen].used) {
				chosen = i
			}
		}
	}
	if chosen < 0 {
		chosen = 0
	}
	cached := common(s.slots[chosen].words, words)
	if pinned < 0 && s.clearIdle {
		for _, h := range s.host {
			if c := common(h, words); c > cached {
				cached = c
			}
		}
	}
	if s.clearIdle {
		for i := range s.slots {
			if i != chosen && !s.slots[i].busy && len(s.slots[i].words) > 0 {
				s.host = append(s.host, s.slots[i].words)
				s.slots[i].words = nil
			}
		}
	}
	s.slots[chosen].busy = true
	s.mu.Unlock()

	time.Sleep(s.pause)

	s.mu.Lock()
	s.slots[chosen].busy = false
	s.slots[chosen].words = append(slices.Clone(words), "ok")
	s.slots[chosen].used = s.tick
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, sse(reply{
		prompt: len(words), completion: 1, text: "ok",
		cacheN: cached, promptN: len(words) - cached, promptMS: float64(len(words)-cached) / 2, timings: true,
		usageCached: &cached,
	}))
}

func TestSlotHintHonouredPassesOnAServerThatReadsIdSlot(t *testing.T) {
	srv := newSlotServer(t, true, false)
	res := live.SlotHintHonoured("fixed", provider.APIChat).Execute(context.Background(), connect(t, srv.URL))
	if !res.Passed {
		t.Fatalf("failed: %s", res.Error)
	}
	if len(res.Calls) != 5 {
		t.Fatalf("calls = %d, want 5", len(res.Calls))
	}
	var named []int
	for _, c := range res.Calls {
		named = append(named, *c.Slot)
	}
	if fmt.Sprint(named) != "[1 1 0 0 7]" {
		t.Errorf("slots named = %v, want 1 1 0 0 and a slot the server does not have", named)
	}
	if len(res.Notes) != 5 {
		t.Errorf("notes = %q, want what /slots showed for each request", res.Notes)
	}
}

func TestSlotHintHonouredFailsOnAServerThatIgnoresIdSlot(t *testing.T) {
	srv := newSlotServer(t, false, false)
	res := live.SlotHintHonoured("fixed", provider.APIChat).Execute(context.Background(), connect(t, srv.URL))
	if res.Passed || !strings.Contains(res.Error, "did not honour id_slot") {
		t.Fatalf("result = passed %v, %q; want a failure that says the server ignored id_slot", res.Passed, res.Error)
	}
}

func TestSlotAffinityPassesWhereNamedSlotsKeepTheirPrefix(t *testing.T) {
	srv := newSlotServer(t, true, false)
	res := live.SlotAffinity("fixed", provider.APIChat).Execute(context.Background(), connect(t, srv.URL))
	if !res.Passed {
		t.Fatalf("failed: %s", res.Error)
	}
	// Six calls with hints, six without.
	if len(res.Calls) != 12 {
		t.Fatalf("calls = %d, want 12", len(res.Calls))
	}
	slot := map[string]int{}
	for _, c := range res.Calls[:6] {
		if c.Slot == nil {
			t.Fatalf("%s named no slot", c.Label)
		}
		session := string(c.Label[len("affine ")])
		if prev, ok := slot[session]; ok && prev != *c.Slot {
			t.Errorf("session %s moved from slot %d to %d", session, prev, *c.Slot)
		}
		slot[session] = *c.Slot
	}
	if slot["A"] == slot["B"] {
		t.Errorf("both sessions on slot %d", slot["A"])
	}
	for _, c := range res.Calls[6:] {
		if c.Slot != nil {
			t.Errorf("%s of the control named slot %d", c.Label, *c.Slot)
		}
	}
	// After a session's first turn, nearly all of its prompt is cached.
	for _, c := range res.Calls[2:6] {
		if c.CacheHitRate == nil || *c.CacheHitRate < 0.9 {
			t.Errorf("%s: hit rate %v, want at least 90%%", c.Label, c.CacheHitRate)
		}
	}
	joined := strings.Join(res.Notes, "\n")
	if !strings.Contains(joined, "slot affinity check: passed") || !strings.Contains(joined, "affinity saved") {
		t.Errorf("notes = %q, want the check's verdict and what affinity saved", res.Notes)
	}
	if ratio := res.Summary.Drift; ratio == nil || *ratio != 0 {
		t.Errorf("drift = %v, want 0: the hint must not change what is counted", ratio)
	}
}

func TestSlotAffinityAgreesWithACheckThatFails(t *testing.T) {
	// llama.cpp with a unified cache clears the idle slot when the other one
	// starts: a named slot is empty, the unnamed requests are restored from
	// the host cache, and the check says so.
	srv := newSlotServer(t, true, true)
	res := live.SlotAffinity("fixed", provider.APIChat).Execute(context.Background(), connect(t, srv.URL))
	if !res.Passed {
		t.Fatalf("failed: %s", res.Error)
	}
	joined := strings.Join(res.Notes, "\n")
	for _, want := range []string{"slot affinity check: failed", "--no-cache-idle-slots", "the harness sends this server no id_slot"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes %q do not say %q", res.Notes, want)
		}
	}
}
