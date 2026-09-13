package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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

// fixture is an agent wired to a scripted provider and a temporary workspace.
type fixture struct {
	agent    *agent.Agent
	provider *providertest.Provider
	events   *recorder
	store    *agent.MemoryStore
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
	r, err := builtin.Registry()
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
	store := agent.NewMemoryStore()
	a := agent.New(p, r, agent.Options{
		Executor:     e,
		Emitter:      rec,
		Store:        store,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff: time.Millisecond,
	})
	return &fixture{agent: a, provider: p, events: rec, store: store, exec: e, session: agent.NewSession("session-1", "workspace-1")}
}

// roles lists the roles of the messages in the conversation.
func roles(msgs []provider.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, string(m.Role))
	}
	return out
}

func TestRunStreamsATextAnswer(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("hello there")})
	if err := f.agent.Run(context.Background(), f.session, "hi"); err != nil {
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

func TestRunExecutesToolCalls(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("writing", providertest.Call("c1", "write", map[string]any{"path": "a.txt", "content": "hello"})),
		providertest.Calls("", providertest.Call("c2", "read", map[string]any{"path": "a.txt"})),
		providertest.Text("the file says hello"),
	})
	if err := f.agent.Run(context.Background(), f.session, "write and read a.txt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := roles(f.session.Conversation.Messages()); strings.Join(got, ",") != "user,assistant,tool,assistant,tool,assistant" {
		t.Fatalf("roles = %v", got)
	}
	data, err := f.exec.ReadFile(context.Background(), "a.txt", executor.ReadOpts{})
	if err != nil {
		t.Fatalf("read workspace file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("file = %q, want %q", data, "hello")
	}

	var call event.ToolCall
	f.events.payloadOf(t, event.TypeToolCall, &call)
	if call.Name != "write" || call.CallID != "c1" {
		t.Errorf("tool.call payload = %+v", call)
	}
	var result event.ToolResult
	f.events.payloadOf(t, event.TypeToolResult, &result)
	if result.IsError || !strings.Contains(result.Content, "wrote 5 bytes") {
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
		providertest.Calls("", providertest.Call("c1", "edit", map[string]any{
			"path": "missing.txt", "old_string": "a", "new_string": "b",
		})),
		providertest.Text("that file does not exist"),
	})
	if err := f.agent.Run(context.Background(), f.session, "edit missing.txt"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := f.session.Conversation.Messages()
	toolMsg := msgs[2]
	if !toolMsg.IsError || !strings.Contains(toolMsg.Content, "edit missing.txt") {
		t.Errorf("tool message = %+v, want a failed result", toolMsg)
	}
	var result event.ToolResult
	f.events.payloadOf(t, event.TypeToolResult, &result)
	if !result.IsError {
		t.Errorf("tool.result payload = %+v, want is_error", result)
	}
}

func TestRunReportsAmbiguousEdit(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "edit", map[string]any{
			"path": "a.txt", "old_string": "same", "new_string": "other",
		})),
		providertest.Text("I need more context"),
	})
	if err := f.exec.WriteFile(context.Background(), "a.txt", []byte("same\nsame\n")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := f.agent.Run(context.Background(), f.session, "edit a.txt"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.session.Conversation.Messages()[2].Content; !strings.Contains(got, "appears 2 times") {
		t.Errorf("tool message = %q, want the occurrence count", got)
	}
}

func TestRunRejectsUnknownTool(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "teleport", map[string]any{})),
		providertest.Text("sorry"),
	})
	if err := f.agent.Run(context.Background(), f.session, "teleport"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.session.Conversation.Messages()[2].Content; !strings.Contains(got, `unknown tool "teleport"`) {
		t.Errorf("tool message = %q", got)
	}
}

func TestSteeringIsDeliveredAfterTheRunningTool(t *testing.T) {
	var f *fixture
	poke := callTool{name: "poke", run: func(context.Context) string {
		f.agent.Steer("stop and explain")
		return "poked"
	}}
	f = newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "poke", map[string]any{})),
		providertest.Text("explaining"),
	}, poke)

	if err := f.agent.Run(context.Background(), f.session, "poke"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := roles(f.session.Conversation.Messages()); strings.Join(got, ",") != "user,assistant,tool,user,assistant" {
		t.Fatalf("roles = %v, want the steering message after the tool result", got)
	}
	if got := f.session.Conversation.Messages()[3].Content; got != "stop and explain" {
		t.Errorf("steering message = %q", got)
	}
	if pending := f.agent.PendingSteering(); len(pending) != 0 {
		t.Errorf("pending steering = %v, want it delivered", pending)
	}
}

func TestFollowUpIsDeliveredAfterTheTurn(t *testing.T) {
	var f *fixture
	queueUp := callTool{name: "queue_up", run: func(context.Context) string {
		f.agent.FollowUp("and now the tests")
		return "queued"
	}}
	f = newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "queue_up", map[string]any{})),
		providertest.Text("done with the first task"),
		providertest.Text("done with the tests"),
	}, queueUp)

	if err := f.agent.Run(context.Background(), f.session, "first task"); err != nil {
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
	all.FollowUp("second")
	all.FollowUp("third")

	if err := all.Run(context.Background(), f.session, "first"); err != nil {
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
	f.agent.FollowUp("later")

	err := f.agent.Run(ctx, f.session, "start")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if pending := f.agent.PendingFollowUps(); len(pending) != 1 || pending[0] != "later" {
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
	f.agent.FollowUp("second question")

	err := f.agent.Run(context.Background(), f.session, "first question")
	if err == nil {
		t.Fatal("Run returned no error")
	}
	if pending := f.agent.PendingFollowUps(); len(pending) != 1 || pending[0] != "second question" {
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
	if err := f.agent.Run(context.Background(), f.session, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.provider.Calls() != 3 {
		t.Errorf("model calls = %d, want 3", f.provider.Calls())
	}
	if got := f.session.Conversation.Messages()[1].Content; got != "finally" {
		t.Errorf("assistant content = %q", got)
	}
}

func TestGivesUpAfterTheRetryBudget(t *testing.T) {
	rateLimited := &provider.Error{Op: "stream", StatusCode: 429, Retryable: true, Err: errors.New("slow down")}
	f := newFixture(t, []providertest.Step{
		providertest.Fail(rateLimited), providertest.Fail(rateLimited),
		providertest.Fail(rateLimited), providertest.Fail(rateLimited),
	})
	err := f.agent.Run(context.Background(), f.session, "hi")
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
	if err := f.agent.Run(context.Background(), f.session, "hi"); err == nil {
		t.Fatal("Run returned no error")
	}
	if f.provider.Calls() != 1 {
		t.Errorf("model calls = %d, want 1", f.provider.Calls())
	}
}

func TestSystemPromptCarriesWorkspaceInstructions(t *testing.T) {
	f := newFixture(t, []providertest.Step{providertest.Text("ok")})
	if err := f.exec.WriteFile(context.Background(), "AGENTS.md", []byte("always run make check")); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := f.agent.Run(context.Background(), f.session, "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	system := f.provider.Requests()[0].System
	if !strings.Contains(system, "You are Eika") {
		t.Error("system prompt is missing the base prompt")
	}
	if !strings.Contains(system, "always run make check") {
		t.Error("system prompt is missing the workspace instructions")
	}
	if len(f.provider.Requests()[0].Tools) == 0 {
		t.Error("request carries no tool definitions")
	}
}

func TestRunRejectsASessionWithoutAConversation(t *testing.T) {
	f := newFixture(t, nil)
	if err := f.agent.Run(context.Background(), &agent.Session{ID: "x"}, "hi"); err == nil {
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

	if err := f.agent.Run(ctx, f.session, "count once"); err == nil {
		t.Fatal("Run returned no error")
	}
	if got := f.session.Conversation.Len(); got != 0 {
		t.Fatalf("conversation holds %d messages after the abort, want 0", got)
	}
	if got := len(f.store.Messages("session-1")); got != 0 {
		t.Fatalf("stored %d messages after the abort, want 0", got)
	}

	if err := f.agent.Run(context.Background(), f.session, "count once"); err != nil {
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
		f.agent.Steer("also check the tests")
		cancel()
		return "poked"
	}}
	f = newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "poke", map[string]any{})),
		providertest.Text("never reached"),
	}, poke)

	if err := f.agent.Run(ctx, f.session, "start"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if pending := f.agent.PendingSteering(); len(pending) != 1 || pending[0] != "also check the tests" {
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
			providertest.Call("c2", "ls", map[string]any{}),
			providertest.Call("c3", "ls", map[string]any{}),
		),
	}, failingTool{})

	if err := f.agent.Run(context.Background(), f.session, "run three tools"); err == nil {
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
			providertest.Call("c2", "ls", map[string]any{}),
		),
	}, stop)

	if err := f.agent.Run(ctx, f.session, "start"); !errors.Is(err, context.Canceled) {
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

func TestToolsWithoutAWorkspaceFailInsteadOfPanicking(t *testing.T) {
	f := newFixture(t, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "ls", map[string]any{})),
		providertest.Text("no workspace then"),
	})
	registry, err := builtin.Registry()
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	noWorkspace := agent.New(f.provider, registry, agent.Options{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryBackoff: time.Millisecond,
	})
	if err := noWorkspace.Run(context.Background(), f.session, "list files"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := f.session.Conversation.Messages()[2]
	if !result.IsError || !strings.Contains(result.Content, "no workspace") {
		t.Errorf("tool message = %+v, want a no-workspace error", result)
	}
}
