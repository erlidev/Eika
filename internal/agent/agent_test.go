package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// recorder collects the events a run emits.
type recorder struct {
	mu     sync.Mutex
	events []event.Event
}

// gatedProvider holds its first response open after one delta. It lets a test
// enqueue a message after the loop's last pre-call queue check.
type gatedProvider struct {
	started chan struct{}
	release chan struct{}

	mu       sync.Mutex
	calls    int
	requests []provider.Request
}

func (p *gatedProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		if call == 1 {
			select {
			case out <- provider.TextDelta("first"):
			case <-ctx.Done():
				return
			}
			close(p.started)
			select {
			case <-p.release:
			case <-ctx.Done():
				return
			}
		} else {
			select {
			case out <- provider.TextDelta("second"):
			case <-ctx.Done():
				return
			}
		}
		select {
		case out <- provider.Done("stop"):
		case <-ctx.Done():
		}
	}()
	return out, nil
}

func (p *gatedProvider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}

// memoryStore keeps messages in memory, keyed by session.
type memoryStore struct {
	mu          sync.Mutex
	sessions    map[string][]provider.Message
	compactions map[string][]agent.Compaction
	// order is what was appended to each session, in order: "message" or
	// "compaction".
	order map[string][]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sessions: map[string][]provider.Message{}, compactions: map[string][]agent.Compaction{}, order: map[string][]string{}}
}

func (s *memoryStore) Append(_ context.Context, sessionID string, m provider.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = append(s.sessions[sessionID], m)
	s.order[sessionID] = append(s.order[sessionID], "message")
	return nil
}

func (s *memoryStore) AppendCompaction(_ context.Context, sessionID string, c agent.Compaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactions[sessionID] = append(s.compactions[sessionID], c)
	s.order[sessionID] = append(s.order[sessionID], "compaction")
	return nil
}

func (s *memoryStore) Messages(sessionID string) []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.sessions[sessionID]...)
}

// strictStore rejects cancelled writes, checks that every message is valid
// persisted JSON, and fails the append numbered failAt, counting from 1.
type strictStore struct {
	mu       sync.Mutex
	failAt   int
	attempts int
	messages []provider.Message
}

func (s *strictStore) Append(ctx context.Context, _ string, m provider.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := json.Marshal(m); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.attempts == s.failAt {
		return errors.New("temporary store failure")
	}
	s.messages = append(s.messages, m)
	return nil
}

func (s *strictStore) AppendCompaction(ctx context.Context, _ string, _ agent.Compaction) error {
	return ctx.Err()
}

func (s *strictStore) Messages() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.messages...)
}

// Emit records one event.
func (r *recorder) Emit(_ context.Context, e event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// all returns the recorded events in order.
func (r *recorder) all() []event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Event(nil), r.events...)
}

// payloadOf returns the first payload of the given event type.
func (r *recorder) payloadOf(t *testing.T, typ string, v any) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Type == typ {
			if err := e.DecodePayload(v); err != nil {
				t.Fatalf("decode %s payload: %v", typ, err)
			}
			return
		}
	}
	t.Fatalf("no %s event in %v", typ, r.events)
}

// callTool is a test tool that runs a function, so that a test can act in the
// middle of a turn.
type callTool struct {
	name string
	run  func(ctx context.Context) string
}

func (c callTool) Name() string        { return c.name }
func (c callTool) Description() string { return "test tool " + c.name }
func (c callTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (c callTool) Call(ctx context.Context, _ tool.CallContext, _ json.RawMessage) (tool.Result, error) {
	return tool.Text(c.run(ctx)), nil
}

// fixture is an agent wired to a scripted provider and a temporary computer.
type fixture struct {
	agent    *agent.Agent
	provider *providertest.Provider
	events   *recorder
	store    *memoryStore
	session  *agent.Session
	exec     *local.Executor
}

// newFixture builds an agent over the built-in tools plus any extra tools.
func newFixture(t *testing.T, steps []providertest.Step, extra ...tool.Tool) *fixture {
	t.Helper()
	return newFixtureWith(t, agent.Options{}, steps, extra...)
}

// newFixtureWith is newFixture with options of the test's own. The fixture
// fills in the ones the test leaves zero.
func newFixtureWith(t *testing.T, opts agent.Options, steps []providertest.Step, extra ...tool.Tool) *fixture {
	t.Helper()
	e, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	for _, x := range extra {
		if err := r.Register(x); err != nil {
			t.Fatalf("register %s: %v", x.Name(), err)
		}
	}
	p := providertest.New(steps...)
	rec := &recorder{}
	store := newMemoryStore()
	opts.Executor, opts.Emitter = e, rec
	if opts.Store == nil {
		opts.Store = store
	}
	if opts.ContextWindow == 0 {
		opts.ContextWindow = 400_000
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.RetryBackoff == 0 {
		opts.RetryBackoff = time.Millisecond
	}
	a := agent.New(p, r, opts)
	return &fixture{agent: a, provider: p, events: rec, store: store, exec: e, session: agent.NewSession("session-1", "computer-1")}
}

// roles lists the roles of the messages in the conversation.
func roles(msgs []provider.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, string(m.Role))
	}
	return out
}

// resultsByBackendID returns the tool results of msgs keyed by the id the
// backend gave the call each one answers. The loop mints its own id for every
// call and keeps the backend's as the call's WireID, so a test that scripted
// the backend ids finds its results by them.
func resultsByBackendID(msgs []provider.Message) map[string]provider.Message {
	backend := map[string]string{}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if c.WireID != nil {
				backend[c.ID] = *c.WireID
			}
		}
	}
	results := map[string]provider.Message{}
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			results[backend[m.ToolCallID]] = m
		}
	}
	return results
}

func TestSteeringAcceptedDuringFinalResponseStartsANewTurn(t *testing.T) {
	p := &gatedProvider{started: make(chan struct{}), release: make(chan struct{})}
	store := newMemoryStore()
	a := agent.New(p, nil, agent.Options{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s := agent.NewSession("session-1", "computer-1")
	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background(), s, provider.UserMessage("first question")) }()
	<-p.started
	if accepted := a.Steer(provider.UserMessage("late steering")); !accepted {
		t.Fatal("Steer rejected a message while the model response was active")
	}
	close(p.release)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(requests))
	}
	if got := requests[1].Messages[len(requests[1].Messages)-1].Content; got != "late steering" {
		t.Errorf("second call ends with %q, want late steering", got)
	}
	if accepted := a.FollowUp(provider.UserMessage("too late")); accepted {
		t.Error("FollowUp accepted a message after the run closed its queues")
	}
}

func TestRunExecutesToolCalls(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("writing", providertest.Call("c1", "bash", map[string]any{"command": "printf hello > a.txt && wc -c a.txt"})),
		providertest.Calls("", providertest.Call("c2", "bash", map[string]any{"command": "cat a.txt"})),
		providertest.Text("the file says hello"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("write and read a.txt")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := roles(f.session.Conversation.Messages()); strings.Join(got, ",") != "user,assistant,tool,assistant,tool,assistant" {
		t.Fatalf("roles = %v", got)
	}
	data, err := f.exec.ReadFile(context.Background(), "a.txt", executor.ReadOpts{})
	if err != nil {
		t.Fatalf("read computer file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("file = %q, want %q", data, "hello")
	}
}

func TestFailedTurnReturnsTheFollowUpToTheQueue(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Text("first answer"),
		providertest.Fail(errors.New("provider is down")),
	})
	f.agent.FollowUp(provider.UserMessage("second question"))

	err := f.agent.Run(context.Background(), f.session, provider.UserMessage("first question"))
	if err == nil {
		t.Fatal("Run returned no error")
	}
	if pending := f.agent.PendingFollowUps(); len(pending) != 1 || pending[0].Content != "second question" {
		t.Errorf("pending follow-ups = %v, want the message back in the queue", pending)
	}
}

func TestRunRejectsAStreamWithoutACompletionEvent(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Stream(provider.TextDelta("partial"))})
	err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi"))
	if err == nil || !strings.Contains(err.Error(), "without a completion event") {
		t.Fatalf("Run error = %v, want missing completion event", err)
	}
	if got := len(f.store.Messages("session-1")); got != 0 {
		t.Errorf("stored messages = %d, want no incomplete response", got)
	}
}

func TestRunDropsToolCallsFromAResponseTheEndpointCutOff(t *testing.T) {
	// A call cut off partway through its arguments must not run, and one left
	// in the message without a result makes the next request malformed.
	f := newFixture(t, []providertest.Step{
		providertest.Stream(
			provider.TextDelta("about to write it"),
			provider.Event{
				Kind:     provider.KindToolCall,
				ToolCall: providertest.Call("call-1", "bash", map[string]string{"command": "touch a.txt"}),
			},
			provider.Done("length"),
		),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("write the file")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := f.session.Conversation.Messages()
	last := msgs[len(msgs)-1]
	if len(last.ToolCalls) != 0 {
		t.Errorf("assistant tool calls = %+v, want the half-written call dropped", last.ToolCalls)
	}
	if last.Content != "about to write it" {
		t.Errorf("assistant content = %q, want the prose that did arrive", last.Content)
	}
	for _, e := range f.events.all() {
		if e.Type == event.TypeToolCall {
			t.Errorf("a tool call ran from a response the endpoint cut off")
		}
	}
}

func TestRunKeepsReasoningItemsAsReturnedAndSendsThemBackOnlyWhenPreserved(t *testing.T) {
	item := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"gAAAA+/==","status":null}`)
	for _, preserve := range []bool{true, false} {
		t.Run(map[bool]string{true: "preserved", false: "not preserved"}[preserve], func(t *testing.T) {
			p := providertest.New(
				providertest.Stream(
					provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "think"},
					provider.Event{Kind: provider.KindReasoningItem, ReasoningItem: item},
					provider.TextDelta("one"),
					provider.Done("stop")),
				providertest.Text("two"))
			a := agent.New(p, nil, agent.Options{
				PreserveThinking: preserve,
				Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			s := agent.NewSession("s1", "w1")
			if err := a.Run(context.Background(), s, provider.UserMessage("first")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			// The conversation always keeps what the endpoint returned, so
			// that a later change of setting can still send it.
			stored := s.Conversation.Messages()[1]
			if stored.Reasoning != "think" || !reflect.DeepEqual(stored.ReasoningItems, provider.ReasoningItems{item}) {
				t.Fatalf("stored message = %+v, want the reasoning text and the item as returned", stored)
			}
			if err := a.Run(context.Background(), s, provider.UserMessage("second")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			sent := p.Requests()[1].Messages[1]
			if preserve && (!reflect.DeepEqual(sent.ReasoningItems, provider.ReasoningItems{item}) || sent.Reasoning != "think") {
				t.Errorf("second request message = %+v, want the reasoning and the item", sent)
			}
			if !preserve && (sent.Reasoning != "" || sent.ReasoningItems != nil) {
				t.Errorf("second request message = %+v, want no reasoning when the model does not preserve it", sent)
			}
		})
	}
}

// failingTool is a tool whose call fails the way a broken harness would, with
// an error rather than a failed result.
type failingTool struct{}

func (failingTool) Name() string        { return "boom" }
func (failingTool) Description() string { return "a tool that breaks the harness" }
func (failingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (failingTool) Call(context.Context, tool.CallContext, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, errors.New("the tool broke")
}

func TestAbortDoesNotDuplicateTheMessageOnTheNextRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFixture(t, []providertest.Step{providertest.Text("answering now")})

	if err := f.agent.Run(ctx, f.session, provider.UserMessage("count once")); err == nil {
		t.Fatal("Run returned no error")
	}
	if got := f.session.Conversation.Len(); got != 0 {
		t.Fatalf("conversation holds %d messages after the abort, want 0", got)
	}
	if got := len(f.store.Messages("session-1")); got != 0 {
		t.Fatalf("stored %d messages after the abort, want 0", got)
	}

	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("count once")); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	seen := 0
	for _, m := range f.session.Conversation.Messages() {
		if m.Role == provider.RoleUser && m.Content == "count once" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the user message appears %d times, want 1", seen)
	}
}

func TestSteeringIsNotReplayedAfterAnAbort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var f *fixture
	poke := callTool{name: "poke", run: func(context.Context) string {
		f.agent.Steer(provider.UserMessage("also check the tests"))
		cancel()
		return "poked"
	}}
	f = newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "poke", map[string]any{})),
		providertest.Text("never reached"),
	}, poke)

	if err := f.agent.Run(ctx, f.session, provider.UserMessage("start")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if pending := f.agent.PendingSteering(); len(pending) != 1 || pending[0].Content != "also check the tests" {
		t.Fatalf("pending steering = %v, want the message back in the queue", pending)
	}
	for _, m := range f.session.Conversation.Messages() {
		if m.Content == "also check the tests" {
			t.Error("the undelivered steering message stayed in the conversation")
		}
	}
}

func TestFailedToolCallAnswersEveryCallInTheBatch(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("",
			providertest.Call("c1", "boom", map[string]any{}),
			providertest.Call("c2", "bash", map[string]any{"command": "ls"}),
			providertest.Call("c3", "bash", map[string]any{"command": "ls"}),
		),
	}, failingTool{})

	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("run three tools")); err == nil {
		t.Fatal("Run returned no error")
	}
	msgs := f.session.Conversation.Messages()
	assistant := msgs[1]
	results := map[string]provider.Message{}
	for _, m := range msgs {
		if m.Role == provider.RoleTool {
			results[m.ToolCallID] = m
		}
	}
	if len(results) != len(assistant.ToolCalls) {
		t.Fatalf("%d tool results for %d tool calls", len(results), len(assistant.ToolCalls))
	}
	for _, c := range assistant.ToolCalls {
		if !results[c.ID].IsError {
			t.Errorf("result for %s = %+v, want a failed result", c.ID, results[c.ID])
		}
	}
}

func TestAbortPersistsATerminalResultForEveryToolCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := callTool{name: "stop_and_cancel", run: func(context.Context) string {
		cancel()
		return "stopped"
	}}
	store := &strictStore{}
	f := newFixtureWith(t, agent.Options{Store: store}, []providertest.Step{providertest.Calls("",
		providertest.Call("c1", "stop_and_cancel", map[string]any{}),
		providertest.Call("c2", "bash", map[string]any{"command": "ls"}),
	)}, stop)
	if err := f.agent.Run(ctx, f.session, provider.UserMessage("start")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	results := resultsByBackendID(store.Messages())
	if len(results) != 2 || results["c1"].Role == "" || results["c2"].Role == "" {
		t.Errorf("persisted tool results = %v, want c1 and c2", results)
	}
}

func TestCompletedToolResultStoreFailurePersistsATerminalResult(t *testing.T) {
	complete := callTool{name: "complete", run: func(context.Context) string { return "completed" }}
	store := &strictStore{failAt: 3}
	f := newFixtureWith(t, agent.Options{Store: store},
		[]providertest.Step{providertest.Calls("", providertest.Call("c1", "complete", map[string]any{}))}, complete)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("run it")); err == nil {
		t.Fatal("Run returned no error")
	}
	for _, messages := range [][]provider.Message{store.Messages(), f.session.Conversation.Messages()} {
		results := resultsByBackendID(messages)
		if len(results) != 1 || !results["c1"].IsError || !strings.Contains(results["c1"].Content, "temporary store failure") {
			t.Errorf("terminal results = %+v, want one persisted c1 error", results)
		}
	}
}

func TestMalformedToolArgumentsBecomeARecoverableToolResult(t *testing.T) {
	call := provider.ToolCall{ID: "c1", Name: "bash", Arguments: provider.ToolArguments(`{"command":`)}
	store := &strictStore{}
	f := newFixtureWith(t, agent.Options{Store: store}, []providertest.Step{providertest.Calls("", call), providertest.Text("recovered")})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("read it")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	messages := store.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored messages = %d, want user, assistant, tool result, assistant", len(messages))
	}
	if !messages[2].IsError || messages[2].ToolCallID != messages[1].ToolCalls[0].ID {
		t.Errorf("tool result = %+v, want a recoverable c1 error", messages[2])
	}
	if messages[3].Content != "recovered" {
		t.Errorf("final assistant message = %+v", messages[3])
	}
}
