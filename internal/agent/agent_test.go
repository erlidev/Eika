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

// checkingStore rejects cancelled writes and verifies that every message is
// valid persisted JSON before it records it.
type checkingStore struct {
	mu       sync.Mutex
	messages []provider.Message
}

func (s *checkingStore) Append(ctx context.Context, _ string, m provider.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := json.Marshal(m); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return nil
}

func (s *checkingStore) AppendCompaction(ctx context.Context, _ string, _ agent.Compaction) error {
	return ctx.Err()
}

func (s *checkingStore) Messages() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.messages...)
}

// failOnceStore rejects one selected append before recording it.
type failOnceStore struct {
	mu       sync.Mutex
	failAt   int
	attempts int
	messages []provider.Message
}

func (s *failOnceStore) Append(ctx context.Context, _ string, m provider.Message) error {
	if err := ctx.Err(); err != nil {
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

func (s *failOnceStore) AppendCompaction(ctx context.Context, _ string, _ agent.Compaction) error {
	return ctx.Err()
}

func (s *failOnceStore) Messages() []provider.Message {
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

// types lists the recorded event types in order.
func (r *recorder) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
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
	a := agent.New(p, r, agent.Options{
		Executor:      e,
		Emitter:       rec,
		Store:         store,
		ContextWindow: 400_000,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff:  time.Millisecond,
	})
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

func TestRunStreamsATextAnswer(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("hello there")})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := f.session.Conversation.Messages()
	if got := roles(msgs); len(got) != 2 || got[0] != "user" || got[1] != "assistant" {
		t.Fatalf("roles = %v, want [user assistant]", got)
	}
	if msgs[1].Content != "hello there" {
		t.Errorf("assistant content = %q", msgs[1].Content)
	}
	if got := len(f.store.Messages("session-1")); got != 2 {
		t.Errorf("stored %d messages, want 2", got)
	}
	want := []string{event.TypeTurnStart, event.TypeMessageDelta, event.TypeTurnEnd}
	if got := f.events.types(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
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

	var call event.ToolCall
	f.events.payloadOf(t, event.TypeToolCall, &call)
	first := f.session.Conversation.Messages()[1].ToolCalls[0]
	if call.Name != "bash" || call.CallID != first.ID || first.ID == "c1" {
		t.Errorf("tool.call payload = %+v, want the id the loop minted for the call, not the backend's c1", call)
	}
	var result event.ToolResult
	f.events.payloadOf(t, event.TypeToolResult, &result)
	if result.IsError || !strings.Contains(result.Content, "5 a.txt") {
		t.Errorf("tool.result payload = %+v", result)
	}
	// The second model call must see the tool result.
	requests := f.provider.Requests()
	if len(requests) != 3 {
		t.Fatalf("model calls = %d, want 3", len(requests))
	}
	if last := requests[1].Messages[len(requests[1].Messages)-1]; last.Role != provider.RoleTool {
		t.Errorf("second request ends with %+v, want the tool result", last)
	}
}

func TestRunSurfacesToolErrorsToTheModel(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "bash", map[string]any{"command": "cat missing.txt"})),
		providertest.Text("that file does not exist"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("read missing.txt")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := f.session.Conversation.Messages()
	toolMsg := msgs[2]
	if !toolMsg.IsError || !strings.Contains(toolMsg.Content, "missing.txt") {
		t.Errorf("tool message = %+v, want a failed result", toolMsg)
	}
	var result event.ToolResult
	f.events.payloadOf(t, event.TypeToolResult, &result)
	if !result.IsError {
		t.Errorf("tool.result payload = %+v, want is_error", result)
	}
}

func TestRunRejectsUnknownTool(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "teleport", map[string]any{})),
		providertest.Text("sorry"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("teleport")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.session.Conversation.Messages()[2].Content; !strings.Contains(got, `unknown tool "teleport"`) {
		t.Errorf("tool message = %q", got)
	}
}

func TestSteeringIsDeliveredAfterTheRunningTool(t *testing.T) {
	var f *fixture
	poke := callTool{name: "poke", run: func(context.Context) string {
		f.agent.Steer(screenshot("stop and explain"))
		return "poked"
	}}
	f = newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "poke", map[string]any{})),
		providertest.Text("explaining"),
	}, poke)

	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("poke")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := roles(f.session.Conversation.Messages()); strings.Join(got, ",") != "user,assistant,tool,user,assistant" {
		t.Fatalf("roles = %v, want the steering message after the tool result", got)
	}
	if got := f.session.Conversation.Messages()[3]; got.Content != "stop and explain" || len(got.Images) != 1 {
		t.Errorf("steering message = %q with %d images, want the text and its image", got.Content, len(got.Images))
	}
	if pending := f.agent.PendingSteering(); len(pending) != 0 {
		t.Errorf("pending steering = %v, want it delivered", pending)
	}
}

func TestFollowUpIsDeliveredAfterTheTurn(t *testing.T) {
	var f *fixture
	queueUp := callTool{name: "queue_up", run: func(context.Context) string {
		f.agent.FollowUp(provider.UserMessage("and now the tests"))
		return "queued"
	}}
	f = newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "queue_up", map[string]any{})),
		providertest.Text("done with the first task"),
		providertest.Text("done with the tests"),
	}, queueUp)

	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("first task")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := roles(f.session.Conversation.Messages()); strings.Join(got, ",") != "user,assistant,tool,assistant,user,assistant" {
		t.Fatalf("roles = %v, want the follow-up after the turn ended", got)
	}
	types := f.events.types()
	var turnEnds, turnStarts int
	for _, typ := range types {
		switch typ {
		case event.TypeTurnStart:
			turnStarts++
		case event.TypeTurnEnd:
			turnEnds++
		}
	}
	if turnStarts != 2 || turnEnds != 2 {
		t.Errorf("turn starts = %d, turn ends = %d, want 2 and 2", turnStarts, turnEnds)
	}
}

func TestQueueModeAllDeliversEveryFollowUp(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("one"), providertest.Text("two")})
	all := agent.New(f.provider, nil, agent.Options{
		Queue:        agent.QueueAll,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff: time.Millisecond,
	})
	all.FollowUp(provider.UserMessage("second"))
	all.FollowUp(provider.UserMessage("third"))

	if err := all.Run(context.Background(), f.session, provider.UserMessage("first")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := roles(f.session.Conversation.Messages()); strings.Join(got, ",") != "user,assistant,user,user,assistant" {
		t.Fatalf("roles = %v, want both follow-ups in one turn", got)
	}
	if pending := all.PendingFollowUps(); len(pending) != 0 {
		t.Errorf("pending follow-ups = %v", pending)
	}
}

func TestAbortLeavesQueuedMessagesInPlace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := callTool{name: "stop", run: func(context.Context) string {
		cancel()
		return "stopped"
	}}
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "stop", map[string]any{})),
		providertest.Text("never reached"),
	}, stop)
	f.agent.FollowUp(provider.UserMessage("later"))

	err := f.agent.Run(ctx, f.session, provider.UserMessage("start"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if pending := f.agent.PendingFollowUps(); len(pending) != 1 || pending[0].Content != "later" {
		t.Errorf("pending follow-ups = %v, want [later]", pending)
	}
	if f.provider.Calls() != 1 {
		t.Errorf("model calls = %d, want the run to stop after the abort", f.provider.Calls())
	}
	var payload event.RunError
	f.events.payloadOf(t, event.TypeRunError, &payload)
	if !strings.Contains(payload.Message, "context canceled") {
		t.Errorf("run.error payload = %+v", payload)
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

func TestRetriesRetryableFailures(t *testing.T) {
	rateLimited := &provider.Error{Op: "stream", StatusCode: 429, Retryable: true, Err: errors.New("slow down")}
	f := newFixture(t, []providertest.Step{
		providertest.Fail(rateLimited),
		providertest.Fail(rateLimited),
		providertest.Text("finally"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.provider.Calls() != 3 {
		t.Errorf("model calls = %d, want 3", f.provider.Calls())
	}
	if got := f.session.Conversation.Messages()[1].Content; got != "finally" {
		t.Errorf("assistant content = %q", got)
	}
}

func TestRetryResetsTextFromTheFailedAttempt(t *testing.T) {
	rateLimited := &provider.Error{Op: "stream", StatusCode: 429, Retryable: true, Err: errors.New("slow down")}
	f := newFixture(t, []providertest.Step{
		providertest.Stream(provider.TextDelta("discard me"), provider.Errorf(rateLimited)),
		providertest.Text("keep me"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{event.TypeTurnStart, event.TypeMessageDelta, event.TypeMessageReset, event.TypeMessageDelta, event.TypeTurnEnd}
	if got := f.events.types(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
	if got := f.session.Conversation.Messages()[1].Content; got != "keep me" {
		t.Errorf("stored assistant content = %q, want only the successful attempt", got)
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

func TestRunRejectsACompletionWithoutAStopReason(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Stream(provider.TextDelta("partial"), provider.Done("")),
	})
	err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi"))
	if err == nil || !strings.Contains(err.Error(), "no stop reason") {
		t.Fatalf("Run error = %v, want missing stop reason", err)
	}
}

func TestRunKeepsAResponseTheEndpointCutOff(t *testing.T) {
	// The user watched this text stream. Throwing it away leaves the model
	// reading its own answer as never given, so the next turn apologises for
	// a turn it did in fact take.
	for _, reason := range []string{"length", "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			f := newFixture(t, []providertest.Step{
				providertest.Stream(provider.TextDelta("partial"), provider.Done(reason)),
			})
			if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			msgs := f.session.Conversation.Messages()
			if len(msgs) != 2 {
				t.Fatalf("conversation = %d messages, want the question and the answer it got", len(msgs))
			}
			if msgs[0].Role != provider.RoleUser || msgs[0].Content != "hi" {
				t.Errorf("first message = %+v, want the user's own message kept", msgs[0])
			}
			if msgs[1].Role != provider.RoleAssistant || msgs[1].Content != "partial" {
				t.Errorf("second message = %+v, want the text the endpoint did produce", msgs[1])
			}

			var end event.TurnEnd
			f.events.payloadOf(t, event.TypeTurnEnd, &end)
			if end.StopReason != reason {
				t.Errorf("turn.end stop_reason = %q, want %q so a client can say the answer was cut off",
					end.StopReason, reason)
			}
		})
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

func TestRunStoresNothingForACutOffResponseThatProducedNothing(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Stream(provider.Done("length"))})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := f.session.Conversation.Messages()
	if len(msgs) != 1 || msgs[0].Role != provider.RoleUser {
		t.Fatalf("conversation = %+v, want the question alone and no empty assistant turn", msgs)
	}
}

func TestContextWindowIsEnforcedBeforeTheProviderCall(t *testing.T) {
	p := providertest.New(providertest.Text("unreachable"))
	a := agent.New(p, nil, agent.Options{
		ContextWindow: 64,
		Sampling:      provider.Sampling{MaxOutput: ptr(32)},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := a.Run(context.Background(), agent.NewSession("s1", "w1"), provider.UserMessage("hello"))
	if err == nil || !strings.Contains(err.Error(), "configured window") {
		t.Fatalf("Run error = %v, want context window error", err)
	}
	if p.Calls() != 0 {
		t.Errorf("provider calls = %d, want no oversized request sent", p.Calls())
	}
}

func TestRunForwardsReasoningConfiguration(t *testing.T) {
	p := providertest.New(providertest.Text("ok"))
	a := agent.New(p, nil, agent.Options{
		Sampling:         provider.Sampling{ReasoningEffort: ptr("none")},
		ThinkingSwitch:   provider.SwitchTemplate,
		PreserveThinking: true,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("s1", "w1")
	s.Conversation.Append(provider.UserMessage("earlier"))
	prior := provider.AssistantMessage("answer", nil)
	prior.Metrics = &provider.MessageMetrics{RunID: "prior", ContextWindow: 8192}
	s.Conversation.Append(prior)
	if err := a.Run(context.Background(), s, provider.UserMessage("hello")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := p.Requests()[0]
	if e := req.Sampling.ReasoningEffort; e == nil || *e != "none" || req.ThinkingSwitch != provider.SwitchTemplate || !req.PreserveThinking {
		t.Errorf("request reasoning = %v switch = %q preserve = %t",
			e, req.ThinkingSwitch, req.PreserveThinking)
	}
	if req.Messages[1].Metrics != nil {
		t.Errorf("provider received stored UI metrics: %+v", req.Messages[1].Metrics)
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
			if stored.Reasoning != "think" || !reflect.DeepEqual(stored.ReasoningItems, []json.RawMessage{item}) {
				t.Fatalf("stored message = %+v, want the reasoning text and the item as returned", stored)
			}
			if err := a.Run(context.Background(), s, provider.UserMessage("second")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			sent := p.Requests()[1].Messages[1]
			if preserve && (!reflect.DeepEqual(sent.ReasoningItems, []json.RawMessage{item}) || sent.Reasoning != "think") {
				t.Errorf("second request message = %+v, want the reasoning and the item", sent)
			}
			if !preserve && (sent.Reasoning != "" || sent.ReasoningItems != nil) {
				t.Errorf("second request message = %+v, want no reasoning when the model does not preserve it", sent)
			}
		})
	}
}

func TestRunKeepsAResponseThatOnlyReasoned(t *testing.T) {
	item := json.RawMessage(`{"id":"rs_1","type":"reasoning","summary":[]}`)
	p := providertest.New(providertest.Stream(provider.Event{Kind: provider.KindReasoningItem, ReasoningItem: item}, provider.Done("length")))
	a := agent.New(p, nil, agent.Options{PreserveThinking: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s := agent.NewSession("s1", "w1")
	if err := a.Run(context.Background(), s, provider.UserMessage("first")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msgs := s.Conversation.Messages(); len(msgs) != 2 || len(msgs[1].ReasoningItems) != 1 {
		t.Errorf("conversation = %+v, want the cut-off reasoning kept", msgs)
	}
}

func TestGivesUpAfterTheRetryBudget(t *testing.T) {
	rateLimited := &provider.Error{Op: "stream", StatusCode: 429, Retryable: true, Err: errors.New("slow down")}
	f := newFixture(t, []providertest.Step{
		providertest.Fail(rateLimited), providertest.Fail(rateLimited),
		providertest.Fail(rateLimited), providertest.Fail(rateLimited),
	})
	err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi"))
	if err == nil {
		t.Fatal("Run returned no error")
	}
	if f.provider.Calls() != 4 {
		t.Errorf("model calls = %d, want the default budget of 3 retries", f.provider.Calls())
	}
	var payload event.RunError
	f.events.payloadOf(t, event.TypeRunError, &payload)
	if !payload.Retryable {
		t.Errorf("run.error payload = %+v, want retryable", payload)
	}
}

func TestDoesNotRetryPermanentFailures(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Fail(errors.New("bad request"))})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err == nil {
		t.Fatal("Run returned no error")
	}
	if f.provider.Calls() != 1 {
		t.Errorf("model calls = %d, want 1", f.provider.Calls())
	}
}

func TestSystemPromptCarriesTheBasePrompt(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("ok")})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	system := f.provider.Requests()[0].System
	if !strings.Contains(system, "You are Eika") {
		t.Error("system prompt is missing the base prompt")
	}
	if strings.Contains(system, "AGENTS.md") {
		t.Error("system prompt names a context file, which nothing reads")
	}
	if len(f.provider.Requests()[0].Tools) == 0 {
		t.Error("request carries no tool definitions")
	}
}

func TestRunRejectsASessionWithoutAConversation(t *testing.T) {
	f := newFixture(t, nil)
	if err := f.agent.Run(context.Background(), &agent.Session{ID: "x"}, provider.UserMessage("hi")); err == nil {
		t.Fatal("Run returned no error")
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

func TestAbortAnswersTheToolCallsItSkips(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := callTool{name: "stop", run: func(context.Context) string {
		cancel()
		return "stopped"
	}}
	f := newFixture(t, []providertest.Step{
		providertest.Calls("",
			providertest.Call("c1", "stop", map[string]any{}),
			providertest.Call("c2", "bash", map[string]any{"command": "ls"}),
		),
	}, stop)

	if err := f.agent.Run(ctx, f.session, provider.UserMessage("start")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	results := 0
	for _, m := range f.session.Conversation.Messages() {
		if m.Role == provider.RoleTool {
			results++
		}
	}
	if results != 2 {
		t.Errorf("%d tool results for 2 tool calls", results)
	}
}

func TestAbortPersistsATerminalResultForEveryToolCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := callTool{name: "stop_and_cancel", run: func(context.Context) string {
		cancel()
		return "stopped"
	}}
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	if err := registry.Register(stop); err != nil {
		t.Fatalf("register stop tool: %v", err)
	}
	p := providertest.New(providertest.Calls("",
		providertest.Call("c1", "stop_and_cancel", map[string]any{}),
		providertest.Call("c2", "bash", map[string]any{"command": "ls"}),
	))
	store := &checkingStore{}
	a := agent.New(p, registry, agent.Options{
		Executor: newFixture(t, nil).exec,
		Store:    store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := a.Run(ctx, agent.NewSession("s1", "w1"), provider.UserMessage("start")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	results := resultsByBackendID(store.Messages())
	if len(results) != 2 || results["c1"].Role == "" || results["c2"].Role == "" {
		t.Errorf("persisted tool results = %v, want c1 and c2", results)
	}
}

func TestCompletedToolResultStoreFailurePersistsATerminalResult(t *testing.T) {
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	if err := registry.Register(callTool{name: "complete", run: func(context.Context) string {
		return "completed"
	}}); err != nil {
		t.Fatalf("register complete tool: %v", err)
	}
	p := providertest.New(providertest.Calls("", providertest.Call("c1", "complete", map[string]any{})))
	store := &failOnceStore{failAt: 3}
	a := agent.New(p, registry, agent.Options{
		Executor: newFixture(t, nil).exec,
		Store:    store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("s1", "w1")
	if err := a.Run(context.Background(), s, provider.UserMessage("run it")); err == nil {
		t.Fatal("Run returned no error")
	}

	for _, messages := range [][]provider.Message{store.Messages(), s.Conversation.Messages()} {
		results := resultsByBackendID(messages)
		if len(results) != 1 || !results["c1"].IsError || !strings.Contains(results["c1"].Content, "temporary store failure") {
			t.Errorf("terminal results = %+v, want one persisted c1 error", results)
		}
	}
}

func TestSteeringStoreFailureRestoresOnlyTheUnstoredMessages(t *testing.T) {
	var a *agent.Agent
	queue := callTool{name: "queue_steering", run: func(context.Context) string {
		a.Steer(provider.UserMessage("stored steering"))
		a.Steer(provider.UserMessage("retry steering"))
		return "queued"
	}}
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	if err := registry.Register(queue); err != nil {
		t.Fatalf("register queue tool: %v", err)
	}
	p := providertest.New(
		providertest.Calls("", providertest.Call("c1", "queue_steering", map[string]any{})),
		providertest.Text("uncommitted answer"),
	)
	store := &failOnceStore{failAt: 5}
	a = agent.New(p, registry, agent.Options{
		Executor: newFixture(t, nil).exec,
		Store:    store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("s1", "w1")
	if err := a.Run(context.Background(), s, provider.UserMessage("start")); err == nil {
		t.Fatal("Run returned no error")
	}
	if pending := a.PendingSteering(); len(pending) != 1 || pending[0].Content != "retry steering" {
		t.Fatalf("pending steering = %v, want only retry steering", pending)
	}
	assertUserMessageCount(t, store.Messages(), "stored steering", 1)
	assertUserMessageCount(t, store.Messages(), "retry steering", 0)
	assertUserMessageCount(t, s.Conversation.Messages(), "stored steering", 1)
	assertUserMessageCount(t, s.Conversation.Messages(), "retry steering", 0)
}

func TestFollowUpStoreFailureRestoresOnlyTheUnstoredMessages(t *testing.T) {
	p := providertest.New(providertest.Text("first answer"), providertest.Text("uncommitted answer"))
	store := &failOnceStore{failAt: 4}
	a := agent.New(p, nil, agent.Options{
		Queue:  agent.QueueAll,
		Store:  store,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	a.FollowUp(provider.UserMessage("stored follow-up"))
	a.FollowUp(provider.UserMessage("retry follow-up"))
	s := agent.NewSession("s1", "w1")
	if err := a.Run(context.Background(), s, provider.UserMessage("start")); err == nil {
		t.Fatal("Run returned no error")
	}
	if pending := a.PendingFollowUps(); len(pending) != 1 || pending[0].Content != "retry follow-up" {
		t.Fatalf("pending follow-ups = %v, want only retry follow-up", pending)
	}
	assertUserMessageCount(t, store.Messages(), "stored follow-up", 1)
	assertUserMessageCount(t, store.Messages(), "retry follow-up", 0)
	assertUserMessageCount(t, s.Conversation.Messages(), "stored follow-up", 1)
	assertUserMessageCount(t, s.Conversation.Messages(), "retry follow-up", 0)
}

func assertUserMessageCount(t *testing.T, messages []provider.Message, content string, want int) {
	t.Helper()
	got := 0
	for _, message := range messages {
		if message.Role == provider.RoleUser && message.Content == content {
			got++
		}
	}
	if got != want {
		t.Errorf("user message %q count = %d, want %d", content, got, want)
	}
}

func TestMalformedToolArgumentsBecomeARecoverableToolResult(t *testing.T) {
	call := provider.ToolCall{
		ID:        "c1",
		Name:      "bash",
		Arguments: provider.ToolArguments(`{"command":`),
	}
	p := providertest.New(providertest.Calls("", call), providertest.Text("recovered"))
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	fixture := newFixture(t, nil)
	store := &checkingStore{}
	a := agent.New(p, registry, agent.Options{
		Executor: fixture.exec,
		Store:    store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := a.Run(context.Background(), agent.NewSession("s1", "w1"), provider.UserMessage("read it")); err != nil {
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

func TestToolsWithoutAComputerFailInsteadOfPanicking(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "bash", map[string]any{"command": "ls"})),
		providertest.Text("no computer then"),
	})
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	noComputer := agent.New(f.provider, registry, agent.Options{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff: time.Millisecond,
	})
	if err := noComputer.Run(context.Background(), f.session, provider.UserMessage("list files")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := f.session.Conversation.Messages()[2]
	if !result.IsError || !strings.Contains(result.Content, "no computer") {
		t.Errorf("tool message = %+v, want a no-computer error", result)
	}
}

// standaloneTool is a callTool that runs without a computer and records
// whether it was handed an executor.
type standaloneTool struct {
	callTool
	sawExecutor *bool
}

func (standaloneTool) Standalone() {}

func (s standaloneTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	*s.sawExecutor = c.Exec != nil
	return s.callTool.Call(ctx, c, raw)
}

func TestAStandaloneToolRunsWithoutAComputer(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "lookup", map[string]any{})),
		providertest.Text("found it"),
	})
	sawExecutor := true
	lookup := standaloneTool{
		callTool:    callTool{name: "lookup", run: func(context.Context) string { return "the answer" }},
		sawExecutor: &sawExecutor,
	}
	registry, err := tool.NewRegistry(lookup)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	chat := agent.New(f.provider, registry, agent.Options{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff: time.Millisecond,
	})
	if err := chat.Run(context.Background(), agent.NewSession("chat-1", ""), provider.UserMessage("look it up")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	messages := f.provider.Requests()[1].Messages
	result := messages[len(messages)-1]
	if result.IsError || result.Content != "the answer" {
		t.Errorf("tool message = %+v, want the tool's answer", result)
	}
	if sawExecutor {
		t.Error("a standalone tool in a chat was handed an executor")
	}
}

func TestTheBasePromptDoesNotPromiseFilesOrCommands(t *testing.T) {
	p := providertest.New(providertest.Text("ok"))
	chat := agent.New(p, nil, agent.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := chat.Run(context.Background(), agent.NewSession("chat-1", ""), provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	system := p.Requests()[0].System
	if !strings.Contains(system, "Use only the tools you are given") {
		t.Errorf("system prompt = %q, want the chat rules", system)
	}
	if strings.Contains(system, "sandboxed computer") {
		t.Errorf("system prompt = %q, still describes a computer", system)
	}
}

func TestRunStreamsReasoningApartFromTheAnswer(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Stream(
		provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "weighing "},
		provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "options"},
		provider.TextDelta("the answer"),
		provider.Done("stop"),
	)})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var reasoning strings.Builder
	for _, e := range f.events.all() {
		if e.Type != event.TypeReasoningDelta {
			continue
		}
		var p event.ReasoningDelta
		if err := e.DecodePayload(&p); err != nil {
			t.Fatalf("decode reasoning.delta payload: %v", err)
		}
		reasoning.WriteString(p.Text)
	}
	if reasoning.String() != "weighing options" {
		t.Errorf("streamed reasoning = %q, want the whole reasoning", reasoning.String())
	}

	msgs := f.session.Conversation.Messages()
	last := msgs[len(msgs)-1]
	if last.Content != "the answer" || last.Reasoning != "weighing options" {
		t.Errorf("assistant message = %+v, want the answer and its reasoning kept apart", last)
	}
}

func TestTurnReportsMeasuredUsageWhileItStreams(t *testing.T) {
	// An endpoint that reports usage per chunk, as vLLM does with continuous
	// usage statistics: every snapshot becomes a turn.progress event.
	f := newFixture(t, []providertest.Step{providertest.Stream(
		provider.TextDelta("one "),
		provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(100, 1, 101)},
		provider.TextDelta("two"),
		provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(100, 2, 102)},
		provider.Done("stop"),
	)})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var progress []event.TurnProgress
	for _, e := range f.events.all() {
		if e.Type != event.TypeTurnProgress {
			continue
		}
		var p event.TurnProgress
		if err := e.DecodePayload(&p); err != nil {
			t.Fatalf("decode turn.progress payload: %v", err)
		}
		progress = append(progress, p)
	}
	if len(progress) != 2 {
		t.Fatalf("turn.progress events = %d, want one per reported usage", len(progress))
	}
	if progress[0].Usage.OutputTokens != provider.Reported(1) || progress[1].Usage.OutputTokens != provider.Reported(2) {
		t.Errorf("progress output tokens = %+v then %+v, want 1 then 2",
			progress[0].Usage.OutputTokens, progress[1].Usage.OutputTokens)
	}
	if progress[1].GenerationMS < progress[0].GenerationMS {
		t.Errorf("generation_ms went backwards: %d then %d", progress[0].GenerationMS, progress[1].GenerationMS)
	}
	if progress[1].ContextWindow != 400_000 {
		t.Errorf("turn.progress context_window = %d, want 400000", progress[1].ContextWindow)
	}

	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Usage.OutputTokens != provider.Reported(2) || end.Usage.TotalTokens != provider.Reported(102) {
		t.Errorf("turn.end usage = %+v, want the last reported snapshot", end.Usage)
	}
	if end.Context.TotalTokens != provider.Reported(102) {
		t.Errorf("turn.end context = %+v, want the last call's own usage", end.Context)
	}
	if end.ContextWindow != 400_000 {
		t.Errorf("turn.end context_window = %d, want 400000", end.ContextWindow)
	}
	if end.GenerationMS < 0 {
		t.Errorf("turn.end generation_ms = %d, want the measured time", end.GenerationMS)
	}
	stored := f.store.Messages(f.session.ID)
	last := stored[len(stored)-1]
	if last.Metrics == nil || last.Metrics.RunID == "" || last.Metrics.Context.TotalTokens != provider.Reported(102) || last.Metrics.ContextWindow != 400_000 {
		t.Errorf("stored assistant metrics = %+v, want the final context measurement", last.Metrics)
	}
}

func TestTurnCarriesTheCachedCountAndSumsOnlyWhatEveryCallReported(t *testing.T) {
	// The first call states how much of its prompt came from the cache and the
	// second does not. The turn's cached total would read as 8 if the second
	// were counted as 0, which says the cache served less than it may have.
	first := providertest.Usage(10, 3, 13)
	first.CachedTokens = provider.Reported(8)
	first.ReasoningTokens = provider.Reported(0)
	f := newFixture(t, []providertest.Step{
		{Events: []provider.Event{
			{Kind: provider.KindToolCall, ToolCall: providertest.Call("c1", "read_file", map[string]any{"path": "missing.txt"})},
			{Kind: provider.KindUsage, Usage: first},
			provider.Done("tool_calls"),
		}},
		providertest.Stream(
			provider.TextDelta("done"),
			provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(20, 4, 24)},
			provider.Done("stop"),
		),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Usage.InputTokens != provider.Reported(30) || end.Usage.TotalTokens != provider.Reported(37) {
		t.Errorf("turn.end usage = %+v, want the counts both calls reported summed", end.Usage)
	}
	if end.Usage.CachedTokens.Reported() || end.Usage.ReasoningTokens.Reported() {
		t.Errorf("turn.end usage = %+v, want the counts one call did not report left unreported", end.Usage)
	}
	if end.Context.CachedTokens.Reported() {
		t.Errorf("turn.end context = %+v, want the last call's own usage, which stated no cached count", end.Context)
	}
}

func TestTurnReportsNoUsageForAnEndpointThatStatesNone(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("hello")})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Usage.Reported() || end.Context.Reported() {
		t.Errorf("turn.end usage = %+v, context = %+v, want nothing reported", end.Usage, end.Context)
	}
	if raw := string(f.events.all()[len(f.events.all())-1].Payload); !strings.Contains(raw, `"input_tokens":null`) {
		t.Errorf("turn.end payload = %s, want an unreported count to be null on the wire", raw)
	}
	stored := f.store.Messages(f.session.ID)
	if m := stored[len(stored)-1].Metrics; m != nil {
		t.Errorf("stored metrics = %+v, want none for a response with no usage", m)
	}
}

func TestTurnLeavesATurnTotalUnreportedWhenOneCallStatedNoUsage(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		{Events: []provider.Event{
			{Kind: provider.KindToolCall, ToolCall: providertest.Call("c1", "read_file", map[string]any{"path": "missing.txt"})},
			provider.Done("tool_calls"),
		}},
		providertest.Stream(
			provider.TextDelta("done"),
			provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(20, 4, 24)},
			provider.Done("stop"),
		),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Usage.Reported() {
		t.Errorf("turn.end usage = %+v, want the turn's cost unreported: its first call stated none", end.Usage)
	}
	if end.Context.TotalTokens != provider.Reported(24) {
		t.Errorf("turn.end context = %+v, want the call that did state its usage", end.Context)
	}
}

func TestTurnSumsGenerationOverEveryModelCall(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		{Events: []provider.Event{
			provider.TextDelta("running it"),
			{Kind: provider.KindToolCall, ToolCall: providertest.Call("c1", "read_file", map[string]any{"path": "missing.txt"})},
			{Kind: provider.KindUsage, Usage: providertest.Usage(10, 3, 13)},
			provider.Done("tool_calls"),
		}},
		providertest.Stream(
			provider.TextDelta("done"),
			provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(20, 4, 24)},
			provider.Done("stop"),
		),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Usage.OutputTokens != provider.Reported(7) || end.Usage.TotalTokens != provider.Reported(37) {
		t.Errorf("turn.end usage = %+v, want both calls summed", end.Usage)
	}
	// The window holds one conversation, not the sum of every prompt in it.
	if end.Context.TotalTokens != provider.Reported(24) {
		t.Errorf("turn.end context = %+v, want the last call's own usage", end.Context)
	}
}

func TestTurnReportsTheEndpointsOwnTimings(t *testing.T) {
	// An endpoint that measures both of its phases, as llama.cpp and vLLM do.
	// The harness passes them through untouched: it has nothing better.
	reported := provider.Timings{
		PromptTokens: 1200, PromptMS: 300,
		DecodeTokens: 256, DecodeMS: 3200,
		Source: provider.TimedByEndpoint,
	}
	f := newFixture(t, []providertest.Step{providertest.Stream(
		provider.TextDelta("answer"),
		provider.Event{
			Kind:    provider.KindUsage,
			Usage:   providertest.Usage(1200, 256, 1456),
			Timings: reported,
		},
		provider.Done("stop"),
	)})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Timings == nil || *end.Timings != reported {
		t.Errorf("turn.end timings = %+v, want the endpoint's own %+v", end.Timings, reported)
	}
	stored := f.store.Messages(f.session.ID)
	last := stored[len(stored)-1]
	if last.Metrics == nil || last.Metrics.Timings == nil || *last.Metrics.Timings != reported {
		t.Errorf("stored assistant timings = %+v, want them kept for replay", last.Metrics)
	}
}

func TestTurnTimesAnEndpointThatReportsNoTimings(t *testing.T) {
	// OpenAI, Anthropic and OpenRouter measure nothing, so the harness times
	// the stream itself. It can only see the generation phase, and only from
	// the first token on, so the first token is not one of the tokens counted.
	f := newFixture(t, []providertest.Step{providertest.Stream(
		provider.TextDelta("answer"),
		provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(40, 9, 49)},
		provider.Done("stop"),
	)})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Timings == nil {
		t.Fatal("turn.end carried no timings, want the harness's own measurement")
	}
	if end.Timings.Source != provider.TimedByHarness {
		t.Errorf("timings source = %q, want %q", end.Timings.Source, provider.TimedByHarness)
	}
	if end.Timings.DecodeTokens != 8 {
		t.Errorf("decode tokens = %d, want the 8 that followed the first", end.Timings.DecodeTokens)
	}
	if end.Timings.HasPrompt() {
		t.Errorf("prompt phase = %+v, want it unmeasured: the harness cannot see it",
			end.Timings)
	}
}

func TestTurnTimesNothingWhenTheResponseIsOneToken(t *testing.T) {
	// One token opens the decode window and closes nothing, so there is no
	// measured interval and no rate to state.
	f := newFixture(t, []providertest.Step{providertest.Stream(
		provider.TextDelta("ok"),
		provider.Event{Kind: provider.KindUsage, Usage: providertest.Usage(40, 1, 41)},
		provider.Done("stop"),
	)})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var end event.TurnEnd
	f.events.payloadOf(t, event.TypeTurnEnd, &end)
	if end.Timings != nil {
		t.Errorf("turn.end timings = %+v, want none", end.Timings)
	}
}
