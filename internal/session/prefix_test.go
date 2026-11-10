package session_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// The prefix-stability test (docs/design/runtime.md section 4). A server's
// prompt cache holds the conversation a request sent, and the next request is
// cheap only if it begins with the very bytes the last one did. The test runs
// the real agent loop and the real providers against a fake server that keeps
// the raw body of every request, through scripted turns, a restart, a resume
// and a compaction, and requires that:
//
//   - each request's messages (chat) or input (responses) begin with the whole
//     array of the request before it, byte for byte, with new items only at the
//     tail, and everything else the request carries (the system prompt, the
//     tools, the template kwargs, the sampling) is the same bytes, but for the
//     fields named in exempt;
//   - the prompt the fake server's template renders from a request begins with
//     the prompt it rendered from the one before.
//
// Across a compaction the array is replaced by design, and the invariant holds
// again from the first request after it.

// exempt are the request fields allowed to differ from one request to the
// next, each with the reason. Nothing else is.
var exempt = map[string]string{
	// fit lowers the output limit to the room the window has left, so it moves
	// as the conversation grows. A server reads it as how many tokens to
	// generate, not as part of the prompt: the fake template ignores it, and
	// the live scenario prefix_across_compaction shows llama.cpp's cache does
	// too.
	"max_completion_tokens": "fit lowers the output limit as the prompt grows",
	"max_output_tokens":     "fit lowers the output limit as the prompt grows",
}

// stepClock is a clock the test moves.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// convStore is what the harness needs of a store: the loop's own interface,
// and the load the server does at the start of every run.
type convStore interface {
	agent.Store
	Load(ctx context.Context, sessionIDD string) (*agent.Session, error)
}

// jsonStore keeps entries the way the session package writes them, as the JSON
// payloads of a tree, and rebuilds a conversation from them. It is the store
// for a machine without Docker: it round-trips every message through the
// encoding the database stores, and none of the database's own normalisation.
type jsonStore struct {
	mu      sync.Mutex
	entries []store.Entry
}

func (s *jsonStore) Append(_ context.Context, _ string, m provider.Message) error {
	e, err := session.MessageEntry(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *jsonStore) AppendCompaction(_ context.Context, _ string, c agent.Compaction) error {
	e, err := session.CompactionEntry(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *jsonStore) Load(_ context.Context, sessionIDD string) (*agent.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, err := session.Messages(s.entries)
	if err != nil {
		return nil, err
	}
	return &agent.Session{ID: sessionIDD, ComputerID: "computer", Conversation: agent.NewConversation(msgs...)}, nil
}

// fnTool is a tool that runs a function. It is standalone, so it runs with no
// computer.
type fnTool struct {
	name string
	run  func(ctx context.Context, args json.RawMessage) string
}

func (f fnTool) Name() string        { return f.name }
func (f fnTool) Description() string { return "test tool " + f.name }
func (f fnTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"},"n":{"type":"number"}}}`)
}
func (f fnTool) Standalone() {}
func (f fnTool) Call(ctx context.Context, _ tool.CallContext, args json.RawMessage) (tool.Result, error) {
	return tool.Text(f.run(ctx, args)), nil
}

// harness runs the scenario: one session, a fake server, and a fresh agent for
// every run, as the server builds one.
type harness struct {
	t         *testing.T
	api       string
	preserve  bool
	srv       *fakeServer
	store     convStore
	clock     *stepClock
	counter   *counter.Counter
	sessionID string

	// cur is the agent of the run in progress, for the tools that queue
	// messages.
	cur *agent.Agent
	// hanging is closed by the hang tool when it starts.
	hanging chan struct{}
	// carried are the messages a finished run left in its queues, which the
	// server hands to the next run (runs.takePending).
	steering, followUps []provider.Message

	mu    sync.Mutex
	drift []counted
}

// counted is what the loop expected of a call and what the server measured.
type counted struct {
	prediction provider.Prediction
	drift      *int
}

func (h *harness) Record(_ context.Context, c agent.ModelCall) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drift = append(h.drift, counted{c.Prediction, c.Drift})
	return nil
}

// restart is the harness process starting over: the counter that lived in
// memory is gone.
func (h *harness) restart() { h.counter = h.newCounter() }

func (h *harness) newProviders() (provider.Provider, provider.ChatEncoder) {
	ep := provider.Endpoint{BaseURL: h.srv.URL + "/v1", APIKey: "k"}
	build := openai.New
	if h.api == "responses" {
		build = openai.NewResponses
	}
	p, err := build(ep)
	if err != nil {
		h.t.Fatalf("build provider: %v", err)
	}
	return p, p.(provider.ChatEncoder)
}

func (h *harness) newCounter() *counter.Counter {
	_, enc := h.newProviders()
	return counter.New(counter.NewRoutes(counter.Config{
		BaseURL: h.srv.URL + "/v1", APIKey: "k", Backend: probe.LlamaCpp, Counting: probe.CountChat,
	}, enc))
}

const (
	scenarioPrompt = "You are Eika, under test."
	maxOutput      = 1500
	// window is small enough that fit has to lower the output limit once the
	// conversation has grown.
	window = 2000
)

func (h *harness) newAgent() *agent.Agent {
	p, _ := h.newProviders()
	registry, err := tool.NewRegistry(
		fnTool{"echo", func(_ context.Context, args json.RawMessage) string {
			return "echo <" + string(args) + "> & caf\u00e9 \"quoted\""
		}},
		fnTool{"note", func(context.Context, json.RawMessage) string {
			h.cur.Steer(provider.UserMessage("steer: also check b"))
			h.cur.FollowUp(provider.UserMessage("then summarise"))
			h.clock.Advance(20 * time.Minute)
			return "queued"
		}},
		fnTool{"hang", func(ctx context.Context, _ json.RawMessage) string {
			h.cur.Steer(provider.UserMessage("do not wait for it"))
			close(h.hanging)
			<-ctx.Done()
			return "interrupted"
		}},
	)
	if err != nil {
		h.t.Fatalf("registry: %v", err)
	}
	effort, temperature, topK, minP, out := provider.EffortNone, 0.7, 20, 0.05, maxOutput
	prompt := scenarioPrompt
	ag := agent.New(p, registry, agent.Options{
		Model: "qwen", ContextWindow: window, PreserveThinking: h.preserve, ImageInput: true,
		Sampling: provider.Sampling{
			MaxOutput: &out, Temperature: &temperature, TopK: &topK, MinP: &minP, ReasoningEffort: &effort,
		},
		ThinkingSwitch: provider.SwitchTemplate,
		BasePrompt:     &prompt,
		Store:          h.store,
		Counter:        h.counter,
		Recorder:       h,
		Clock:          h.clock.Now,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff:   time.Millisecond,
		Limits:         agent.Limits{Stall: 150 * time.Millisecond},
		Compaction:     agent.CompactionSettings{KeepRecentTokens: 400},
	})
	for _, m := range h.steering {
		ag.Steer(m)
	}
	for _, m := range h.followUps {
		ag.FollowUp(m)
	}
	h.steering, h.followUps = nil, nil
	return ag
}

// run loads the session from the store, as the server does at the start of a
// run, and runs a fresh agent on it.
func (h *harness) run(ctx context.Context, msg provider.Message) error {
	s, err := h.store.Load(ctx, h.sessionID)
	if err != nil {
		h.t.Fatalf("load session: %v", err)
	}
	ag := h.newAgent()
	h.cur = ag
	err = ag.Run(ctx, s, msg)
	h.steering, h.followUps = ag.PendingSteering(), ag.PendingFollowUps()
	return err
}

func (h *harness) compact(ctx context.Context) {
	h.t.Helper()
	s, err := h.store.Load(ctx, h.sessionID)
	if err != nil {
		h.t.Fatalf("load session: %v", err)
	}
	if err := h.newAgent().Compact(ctx, s, ""); err != nil {
		h.t.Fatalf("compact: %v", err)
	}
}

func (h *harness) mustRun(msg provider.Message) {
	h.t.Helper()
	if err := h.run(context.Background(), msg); err != nil {
		h.t.Fatalf("run %q: %v", msg.Content, err)
	}
}

// picture is a tiny image to send with the first message.
func picture() provider.Image {
	return provider.Image{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nnot really a picture\x00\xff"), Width: 16, Height: 16}
}

// summaryText is what the scripted compaction summary says.
const summaryText = "## Goal\nCheck a and b.\n\n## Progress\n### Done\n- [x] Checked a and b"

// scenario runs the scripted conversation and returns the requests the server
// received, with the compaction it made.
func scenario(t *testing.T, api string, preserve bool, st convStore, sessionIDD string) ([]modelCall, *harness) {
	t.Helper()
	h := &harness{
		t: t, api: api, preserve: preserve, srv: newFakeServer(t), store: st, sessionID: sessionIDD,
		clock:   &stepClock{t: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)},
		hanging: make(chan struct{}),
	}
	h.counter = h.newCounter()
	ctx := context.Background()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for i, c := range h.srv.recorded() {
			w := parseBody(t, api, c.body)
			t.Logf("request %d: %s, %d items, last %.110s", i+1, c.step.kind, len(w.items), w.items[len(w.items)-1])
		}
	})

	h.srv.script(
		// Run 1: parallel calls that share a backend id, a call with none, a
		// steering message and a follow-up joining mid-run, and a follow-up
		// whose first attempt fails.
		step{kind: stepTurn, reasoning: "Two checks: a < b && c > d.", text: "Checking both.", calls: []wireCall{
			{"call_0", "echo", `{"text": "a", "n": 1.50}`},
			{"call_0", "echo", `{"n":2,"text":"b <&>"}`},
		}},
		step{kind: stepTurn, reasoning: "Now note it.", calls: []wireCall{{"", "note", `{}`}}},
		step{kind: stepTurn, text: "Steering received."},
		step{kind: stepFail, status: 500},
		step{kind: stepTurn, reasoning: "Summarising.", text: "Summary done."},
		// Run 2, after a restart and three hours: a response cut off at the limit.
		step{kind: stepTurn, reasoning: "Long.", text: "Once upon", cutOff: true},
		// Run 3: an attempt that stalls, then the retry.
		step{kind: stepFail, stall: true},
		step{kind: stepTurn, text: "a time."},
		// Run 4: a call the provider refuses.
		step{kind: stepReject, status: 400},
		// Run 5: a run interrupted while a tool runs.
		step{kind: stepTurn, reasoning: "Hang.", calls: []wireCall{
			{"call_0", "hang", `{}`}, {"call_1", "echo", `{"text":"never"}`},
		}},
		// Run 6, after a restart: the interrupted turn resumed.
		step{kind: stepTurn, text: "Resumed without waiting."},
		// The compaction.
		step{kind: stepSummary, text: summaryText},
		// Run 7: a tool round after the compaction.
		step{kind: stepTurn, reasoning: "Check c.", calls: []wireCall{{"call_0", "echo", `{"text":"c"}`}}},
		step{kind: stepTurn, text: "c checked."},
		// Run 8, after a restart.
		step{kind: stepTurn, reasoning: "Last.", text: "Done."},
	)

	first := provider.UserMessage("Look at the picture, and check a and b.")
	first.Images = []provider.Image{picture()}
	h.mustRun(first)

	h.restart()
	h.clock.Advance(3 * time.Hour)
	h.mustRun(provider.UserMessage("Write something long."))
	h.clock.Advance(40 * time.Second)
	h.mustRun(provider.UserMessage("Go on."))
	h.clock.Advance(2 * time.Minute)
	if err := h.run(ctx, provider.UserMessage("This one is refused.")); err == nil {
		t.Fatal("the refused call did not fail the run")
	}
	h.clock.Advance(time.Minute)

	// An interrupted run: the user stops it while the tool runs.
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- h.run(runCtx, provider.UserMessage("Try again, and use the hang tool.")) }()
	select {
	case <-h.hanging:
	case <-time.After(10 * time.Second):
		t.Fatal("the hang tool never started")
	}
	cancel()
	if err := <-done; err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("the interrupted run returned %v, want a cancellation", err)
	}
	if len(h.steering) != 1 {
		t.Fatalf("the interrupted run left %d steering messages in its queue, want the one sent during the tool", len(h.steering))
	}

	// The harness restarts, and the turn resumes: a run with no new message
	// answers the conversation as it stands, with the steering message that
	// was waiting.
	h.restart()
	h.clock.Advance(15 * time.Minute)
	h.mustRun(provider.Message{})

	h.clock.Advance(10 * time.Minute)
	h.compact(ctx)
	h.clock.Advance(time.Minute)
	h.mustRun(provider.UserMessage("After the compaction, check c."))
	h.restart()
	h.clock.Advance(26 * time.Hour)
	h.mustRun(provider.UserMessage("One more thing."))

	if !h.srv.drained() {
		t.Error("the scripted responses were not all used")
	}
	return h.srv.recorded(), h
}

// wireBody is a request body split into the array that carries the
// conversation and the other fields, each kept as the bytes that were sent.
type wireBody struct {
	items  []json.RawMessage
	fields map[string]json.RawMessage
	key    string
	raw    []byte
}

// itemsKey is the field of a request that holds the conversation.
func itemsKey(api string) string {
	if api == "responses" {
		return "input"
	}
	return "messages"
}

func parseBody(t *testing.T, api string, raw []byte) wireBody {
	t.Helper()
	w := wireBody{key: itemsKey(api), raw: raw}
	if err := json.Unmarshal(raw, &w.fields); err != nil {
		t.Fatalf("a request body is not a JSON object: %v\n%s", err, raw)
	}
	if err := json.Unmarshal(w.fields[w.key], &w.items); err != nil {
		t.Fatalf("a request has no %s array: %v\n%s", w.key, err, raw)
	}
	return w
}

// differing lists the fields, other than the conversation and the exempt
// ones, whose bytes differ between two requests, or are present in one only.
func differing(a, b wireBody) []string {
	var out []string
	names := map[string]bool{}
	for name := range a.fields {
		names[name] = true
	}
	for name := range b.fields {
		names[name] = true
	}
	for name := range names {
		if _, ok := exempt[name]; ok || name == a.key {
			continue
		}
		if !bytes.Equal(a.fields[name], b.fields[name]) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// firstDifference describes where two byte strings first differ.
func firstDifference(a, b []byte) string {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	window := func(s []byte) string {
		return fmt.Sprintf("%q", s[max(i-40, 0):min(i+60, len(s))])
	}
	return fmt.Sprintf("first difference at byte %d:\n    was  %s\n    now  %s", i, window(a), window(b))
}

// extends reports how next fails to extend base: its other fields are the
// same bytes and its items begin with all of base's items.
func extends(base, next wireBody) []string {
	var problems []string
	if fields := differing(base, next); len(fields) > 0 {
		problems = append(problems, fmt.Sprintf("these fields changed: %v", fields))
		for _, name := range fields {
			problems = append(problems, fmt.Sprintf("  %s: %s", name, firstDifference(base.fields[name], next.fields[name])))
		}
	}
	if len(next.items) < len(base.items) {
		problems = append(problems, fmt.Sprintf("%s has %d items, fewer than the %d before it", base.key, len(next.items), len(base.items)))
	}
	for i := 0; i < min(len(base.items), len(next.items)); i++ {
		if !bytes.Equal(base.items[i], next.items[i]) {
			problems = append(problems, fmt.Sprintf("item %d of %s is not the bytes it was:\n  %s", i, base.key, firstDifference(base.items[i], next.items[i])))
			break
		}
	}
	return problems
}

// relation says how each request stands to the ones before it.
type relation struct {
	index int
	call  modelCall
	body  wireBody
	// base is the request this one must extend: the last whose response the
	// loop accepted. Nil for the first request, and for a summary request.
	base *wireBody
	// retryOf is the failed attempt this request repeats, when it is a retry.
	retryOf *wireBody
	// summaryOf is the request a summary request shares its prefix with.
	summaryOf *wireBody
	// afterCompaction is set on the requests between a compaction and the first
	// one the loop accepts: their array begins with the summary.
	afterCompaction bool
	// compactedFrom is the request the compaction left, for those.
	compactedFrom *wireBody
}

// relations walks the requests in order and says what each one must be to
// the others. A failed attempt and a refused call extend the last accepted
// request, as they are sent from the same conversation; a retry repeats the
// failed attempt; a summary shares the prefix of the request before it; and
// the requests after a compaction restart the chain.
func relations(t *testing.T, api string, calls []modelCall) []relation {
	t.Helper()
	var out []relation
	var base, failed, compactedFrom *wireBody
	compacted := false
	for i, c := range calls {
		body := parseBody(t, api, c.body)
		r := relation{index: i, call: c, body: body}
		if c.step.kind == stepSummary {
			r.summaryOf = base
			out = append(out, r)
			compacted, compactedFrom = true, base
			continue
		}
		r.base, r.retryOf = base, failed
		r.afterCompaction, r.compactedFrom = compacted, compactedFrom
		failed = nil
		switch c.step.kind {
		case stepTurn:
			b := body
			base = &b
			compacted = false
		case stepFail:
			b := body
			failed = &b
		}
		out = append(out, r)
	}
	return out
}

func TestEachRequestBeginsWithTheBytesOfTheOneBeforeItJSONEntries(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		for _, preserve := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/preserve=%v", api, preserve), func(t *testing.T) {
				checkPrefixStability(t, api, preserve, &jsonStore{}, "session")
			})
		}
	}
}

// checkPrefixStability runs the scenario on st and checks every request.
func checkPrefixStability(t *testing.T, api string, preserve bool, st convStore, sessionIDD string) {
	calls, h := scenario(t, api, preserve, st, sessionIDD)
	rels := relations(t, api, calls)
	if len(rels) < 15 {
		t.Fatalf("the server saw %d requests, want the whole script", len(rels))
	}

	t.Run("bytes", func(t *testing.T) {
		limit := "max_completion_tokens"
		if api == "responses" {
			limit = "max_output_tokens"
		}
		lowered := false
		for _, r := range rels {
			if r.base != nil && !bytes.Equal(r.base.fields[limit], r.body.fields[limit]) {
				lowered = true
			}
		}
		if !lowered {
			t.Errorf("fit never lowered %s, so the test does not show that the field may vary", limit)
		}
		for _, r := range rels {
			switch {
			case r.call.step.kind == stepSummary:
				checkSummary(t, r)
			case r.afterCompaction:
				checkAfterCompaction(t, api, r)
			case r.base != nil:
				if problems := extends(*r.base, r.body); len(problems) > 0 {
					t.Errorf("request %d (%s) does not begin with the bytes of request %d:\n  %s",
						r.index+1, r.call.step.kind, lastTurn(rels, r.index)+1, strings.Join(problems, "\n  "))
				}
			}
			if r.retryOf != nil {
				if problems := extends(*r.retryOf, r.body); len(problems) > 0 || len(r.retryOf.items) != len(r.body.items) {
					t.Errorf("request %d repeats failed request %d but is not the same bytes:\n  %s",
						r.index+1, r.index, strings.Join(problems, "\n  "))
				}
			}
		}
	})

	t.Run("rendered", func(t *testing.T) {
		for _, r := range rels {
			rendered, _ := promptOf(r.call.path, r.call.body)
			if r.call.step.kind != stepSummary && r.call.counted != nil {
				// The prompt the token counter had the server render is the
				// prompt the call reads, which under responses is also the
				// server's conversion of its input into chat.
				// The counter prices pictures apart and renders the request without
				// them, so they are taken out of the call's prompt to compare.
				if counted, _ := renderChat(r.call.counted); counted != withoutImages(rendered) {
					t.Errorf("request %d: the counted prompt is not the prompt the call reads:\n%s", r.index+1,
						firstDifference([]byte(counted), []byte(withoutImages(rendered))))
				}
			}
			if r.base == nil || r.call.step.kind == stepSummary || r.afterCompaction {
				continue
			}
			before, generation := promptOf(r.call.path, r.base.raw)
			before = strings.TrimSuffix(before, generation)
			if !strings.HasPrefix(rendered, before) {
				t.Errorf("request %d: the rendered prompt does not begin with the one before it:\n%s", r.index+1,
					firstDifference([]byte(before), []byte(rendered)))
			}
		}
	})

	t.Run("counting", func(t *testing.T) {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, c := range h.drift {
			if c.drift != nil && c.prediction.Method == provider.MethodExact && *c.drift != 0 {
				t.Errorf("call %d: exact prediction of %d tokens drifted by %d", i+1, c.prediction.Tokens, *c.drift)
			}
		}
	})
}

// withoutImages takes the pictures out of a rendered prompt.
func withoutImages(prompt string) string { return imageMark.ReplaceAllString(prompt, "") }

var imageMark = regexp.MustCompile(`<\|image\|>[0-9a-f]{8}\n\n`)

// lastTurn is the index of the accepted request before request i.
func lastTurn(rels []relation, i int) int {
	for j := i - 1; j >= 0; j-- {
		if rels[j].call.step.kind == stepTurn {
			return j
		}
	}
	return -1
}

// checkSummary checks a summary request: the cached form of
// docs/design/runtime.md section 11 is the request before it, cut at the end
// of the history the summary covers, and the summary prompt appended. What it
// shares with that request must be the same bytes, or the server reads the
// whole prompt again for the summary.
func checkSummary(t *testing.T, r relation) {
	t.Helper()
	if r.summaryOf == nil {
		t.Errorf("request %d is a summary with nothing before it", r.index+1)
		return
	}
	if fields := differing(*r.summaryOf, r.body); len(fields) > 0 {
		t.Errorf("summary request %d changed these fields from the request it summarises: %v", r.index+1, fields)
	}
	shared := r.body.items[:len(r.body.items)-1]
	if len(shared) > len(r.summaryOf.items) {
		t.Errorf("summary request %d holds %d items before its prompt, more than the %d of the request before it",
			r.index+1, len(shared), len(r.summaryOf.items))
		return
	}
	for i, item := range shared {
		if !bytes.Equal(item, r.summaryOf.items[i]) {
			t.Errorf("summary request %d: item %d is not the bytes it was in the request before:\n  %s",
				r.index+1, i, firstDifference(r.summaryOf.items[i], item))
			return
		}
	}
}

// firstContent is the content field of a message or an input item.
func firstContent(item json.RawMessage) json.RawMessage {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(item, &m)
	return m.Content
}

// checkAfterCompaction checks a request sent after a compaction. The
// compaction replaced everything the conversation held before the messages it
// kept with the summary, so the array does not extend the one before it. What
// does not change is everything beside the array, and the system prompt of a
// chat request, and the array begins with the summary, which carries the text
// the compaction made.
func checkAfterCompaction(t *testing.T, api string, r relation) {
	t.Helper()
	if fields := differing(*r.compactedFrom, r.body); len(fields) > 0 {
		t.Errorf("request %d, the first after the compaction, changed these fields: %v", r.index+1, fields)
	}
	items := r.body.items
	if api == "chat" {
		if len(items) == 0 || !bytes.Equal(items[0], r.compactedFrom.items[0]) {
			t.Errorf("request %d: the system message changed across the compaction", r.index+1)
			return
		}
		items = items[1:]
	}
	if len(items) == 0 || !strings.Contains(contentText(firstContent(items[0])), "<summary>\n"+summaryText) {
		t.Errorf("request %d: the array does not begin with the compaction's summary", r.index+1)
	}
	if len(r.body.items) >= len(r.compactedFrom.items) {
		t.Errorf("request %d has %d items after the compaction, no fewer than the %d it replaced",
			r.index+1, len(r.body.items), len(r.compactedFrom.items))
	}
}
