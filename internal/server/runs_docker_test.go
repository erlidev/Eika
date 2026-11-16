//go:build docker

package server_test

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// askStep is a model response that calls ask_user, which is how these tests
// hold a run open long enough to act on it.
func askStep(callID, question string) providertest.Step {
	return providertest.Calls("", providertest.Call(callID, "ask_user", map[string]any{"question": question}))
}

// session sets up a computer and a session to run in.
func (a *api) session(t *testing.T) sessionWire {
	t.Helper()
	cx := a.home(t)
	return a.newSession(t, cx.ID)
}

// postMessage sends one message to a session.
func (a *api) postMessage(t *testing.T, sessionID, text, mode string, status int) runWire {
	t.Helper()
	body := map[string]any{"text": text}
	if mode != "" {
		body["mode"] = mode
	}
	rec := request(t, a.Server, "POST", "/api/sessions/"+sessionID+"/messages", body)
	if status != 202 {
		if rec.Code != status {
			t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
		}
		return runWire{}
	}
	return decodeBody[runWire](t, rec, 202)
}

// userMessages returns the text of every user message the provider was sent
// on its last call, which is how a test sees what a queue delivered.
func userMessages(req provider.Request) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == provider.RoleUser {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestRunDeliversSteeringAndFollowUpMessages(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)

	// Call one holds the run open on a question. The steering message joins
	// the conversation when the call finishes; the follow-up starts a turn of
	// its own after this one ends.
	p := a.script(
		askStep("c1", "carry on?"),
		providertest.Text("done with the first turn"),
		providertest.Text("done with the follow-up"),
	)
	run := a.postMessage(t, sess.ID, "start", "", 202)
	if run.State != "running" {
		t.Fatalf("run = %+v, want a running run", run)
	}

	question := a.waitQuestion(t, sess.ID)
	a.postMessage(t, sess.ID, "also check the tests", "steer", 202)
	a.postMessage(t, sess.ID, "then write it up", "follow_up", 202)

	state := a.runState(t, sess.ID)
	if len(state.PendingSteering) != 1 || state.PendingSteering[0].Text != "also check the tests" {
		t.Errorf("pending steering = %v", state.PendingSteering)
	}
	if len(state.PendingFollowUps) != 1 || state.PendingFollowUps[0].Text != "then write it up" {
		t.Errorf("pending follow-ups = %v", state.PendingFollowUps)
	}

	if rec := request(t, a.Server, "POST", "/api/questions/"+question.ID+"/answer",
		map[string]any{"answer": "yes"}); rec.Code != 204 {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body.String())
	}

	final := a.waitIdle(t, sess.ID)
	if final.Run == nil || final.Run.State != "done" {
		t.Fatalf("run = %+v, want done", final.Run)
	}
	if p.Calls() != 3 {
		t.Fatalf("model calls = %d, want 3", p.Calls())
	}
	requests := p.Requests()
	if got := userMessages(requests[1]); len(got) != 2 || got[1] != "also check the tests" {
		t.Errorf("second call saw %v, want the steering message delivered", got)
	}
	if got := userMessages(requests[2]); got[len(got)-1] != "then write it up" {
		t.Errorf("third call saw %v, want the follow-up last", got)
	}

	// Everything the run said is in the tree, the answer included.
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	var kinds []string
	for _, e := range path.Entries {
		kinds = append(kinds, e.Kind)
	}
	joined := strings.Join(kinds, " ")
	if !strings.Contains(joined, "tool_result") {
		t.Errorf("entry kinds = %q, want the answer recorded as a tool result", joined)
	}
}

func TestRunAbortStopsTheLoopAndRecordsIt(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(askStep("c1", "are you still there?"), providertest.Text("never reached"))

	run := a.postMessage(t, sess.ID, "start", "", 202)
	question := a.waitQuestion(t, sess.ID)

	aborted := decodeBody[runWire](t, request(t, a.Server, "POST", "/api/runs/"+run.ID+"/abort", nil), 200)
	if aborted.State != "aborted" {
		t.Fatalf("state = %q, want aborted", aborted.State)
	}
	state := a.runState(t, sess.ID)
	if state.Active {
		t.Error("the session still reports an active run after the abort")
	}
	if len(state.Questions) != 0 {
		t.Error("the abandoned question is still pending")
	}

	// The abandoned question cannot be answered any more.
	if rec := request(t, a.Server, "POST", "/api/questions/"+question.ID+"/answer",
		map[string]any{"answer": "hello?"}); rec.Code != 404 {
		t.Errorf("answering an abandoned question = %d, want 404", rec.Code)
	}
	// Aborting a run that is already over is a conflict, not a second abort.
	if rec := request(t, a.Server, "POST", "/api/runs/"+run.ID+"/abort", nil); rec.Code != 409 {
		t.Errorf("second abort = %d, want 409", rec.Code)
	}
	if rec := request(t, a.Server, "POST", "/api/runs/nope/abort", nil); rec.Code != 404 {
		t.Errorf("abort of an unknown run = %d, want 404", rec.Code)
	}
}

func TestAbortKeepsAcceptedMessagesForTheNextRun(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(askStep("c1", "wait here"))

	run := a.postMessage(t, sess.ID, "first run", "", 202)
	a.waitQuestion(t, sess.ID)
	a.postMessage(t, sess.ID, "still do this", "follow_up", 202)
	decodeBody[runWire](t, request(t, a.Server, "POST", "/api/runs/"+run.ID+"/abort", nil), 200)

	state := a.runState(t, sess.ID)
	if len(state.PendingFollowUps) != 1 || state.PendingFollowUps[0].Text != "still do this" {
		t.Fatalf("pending follow-ups = %v, want the accepted message", state.PendingFollowUps)
	}
	p := a.script(providertest.Text("new answer"), providertest.Text("queued answer"))
	a.postMessage(t, sess.ID, "second run", "", 202)
	final := a.waitIdle(t, sess.ID)
	if final.Run == nil || final.Run.State != "done" {
		t.Fatalf("run = %+v, want done", final.Run)
	}
	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(requests))
	}
	got := userMessages(requests[1])
	if got[len(got)-1] != "still do this" {
		t.Errorf("second turn saw %v, want accepted follow-up last", got)
	}
}

func TestCompactRunsAsARunOfItsOwn(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	p := a.script(providertest.Text("first answer"), providertest.Text("second answer"),
		providertest.Text("SUMMARY"), providertest.Text("after"))
	for _, text := range []string{"one", "two"} {
		a.postMessage(t, sess.ID, text+" "+strings.Repeat("x", 8000), "", 202)
		a.waitIdle(t, sess.ID)
	}

	// The default keeps 20,000 tokens, which is all of the conversation.
	compact := func(body map[string]any) *httptest.ResponseRecorder {
		return request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/compact", body)
	}
	if rec := compact(nil); rec.Code != 409 {
		t.Fatalf("compact with nothing to summarize = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"compaction": map[string]any{"keep_recent_tokens": 1024},
	}); rec.Code != 200 {
		t.Fatalf("set compaction = %d: %s", rec.Code, rec.Body.String())
	}
	run := decodeBody[runWire](t, compact(map[string]any{"instructions": "the plan"}), 202)
	state := a.waitIdle(t, sess.ID)
	if state.Run == nil || state.Run.ID != run.ID || state.Run.State != "done" {
		t.Fatalf("run = %+v, want the compaction done", state.Run)
	}
	summary := p.Requests()[2]
	if got := summary.Messages[len(summary.Messages)-1].Content; !strings.HasSuffix(got, "\n\nAdditional focus: the plan") {
		t.Errorf("summary prompt = %q, want the instructions as its focus", got)
	}

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	if last := path.Entries[len(path.Entries)-1]; last.Kind != "compaction" {
		t.Errorf("last entry = %+v, want the compaction", last)
	}
	if len(path.Messages) != 3 || !path.Messages[0].Summary || !strings.HasPrefix(path.Messages[1].Content, "two") {
		t.Errorf("path messages = %+v, want the summary and the second turn", path.Messages)
	}

	a.postMessage(t, sess.ID, "three", "", 202)
	a.waitIdle(t, sess.ID)
	if got := p.Requests()[3].Messages[0].Content; !strings.Contains(got, "<summary>\nSUMMARY\n</summary>") {
		t.Errorf("the run after the compaction began with %q, want the summary", got)
	}
}

func TestOnlyOneOfTwoConcurrentRunsStarts(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(providertest.Text("one answer"))

	// Building a run reads the database and runs a command in the computer.
	// The delay stands in for that: without a claim taken when the check is
	// made, both requests get through it.
	a.mu.Lock()
	a.delay = 150 * time.Millisecond
	a.mu.Unlock()

	const requests = 4
	codes := make(chan int, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages",
				map[string]any{"text": "start"})
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)

	accepted, conflicts := 0, 0
	for code := range codes {
		switch code {
		case 202:
			accepted++
		case 409:
			conflicts++
		default:
			t.Errorf("status = %d, want 202 or 409", code)
		}
	}
	if accepted != 1 || conflicts != requests-1 {
		t.Fatalf("%d accepted and %d refused, want 1 and %d", accepted, conflicts, requests-1)
	}

	a.waitIdle(t, sess.ID)
	runs, err := a.store.Runs(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("run rows = %d, want 1", len(runs))
	}
}
