//go:build docker

package server_test

import (
	"errors"
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

// session sets up a project, a workspace, and a session to run in.
func (a *api) session(t *testing.T) sessionWire {
	t.Helper()
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)
	return a.newSession(t, ws.ID)
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
	if len(state.PendingSteering) != 1 || state.PendingSteering[0] != "also check the tests" {
		t.Errorf("pending steering = %v", state.PendingSteering)
	}
	if len(state.PendingFollowUps) != 1 || state.PendingFollowUps[0] != "then write it up" {
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

func TestRunRefusesASecondRunOnTheSameSession(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(askStep("c1", "wait here"), providertest.Text("done"))

	run := a.postMessage(t, sess.ID, "start", "", 202)
	question := a.waitQuestion(t, sess.ID)

	rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "again"})
	if rec.Code != 409 {
		t.Fatalf("second run = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code, _ := errorOf(t, rec); code != "conflict" {
		t.Errorf("code = %q, want conflict", code)
	}

	// Moving the head is refused for the same reason: the run is writing.
	if rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/head",
		map[string]any{"entry_id": "whatever"}); rec.Code != 409 {
		t.Errorf("set head during a run = %d, want 409", rec.Code)
	}

	request(t, a.Server, "POST", "/api/questions/"+question.ID+"/answer", map[string]any{"answer": "ok"})
	a.waitIdle(t, sess.ID)
	if run.ID == "" {
		t.Error("the run row has no id")
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

func TestRunRejectsBadMessages(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)

	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"no text", map[string]any{"text": "  "}, 400},
		{"an unknown mode", map[string]any{"text": "hi", "mode": "shout"}, 400},
		{"an unknown field", map[string]any{"text": "hi", "colour": "red"}, 400},
		{"steering with no run", map[string]any{"text": "hi", "mode": "steer"}, 409},
		{"a follow-up with no run", map[string]any{"text": "hi", "mode": "follow_up"}, 409},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", c.body)
			if rec.Code != c.status {
				t.Errorf("status = %d, want %d: %s", rec.Code, c.status, rec.Body.String())
			}
		})
	}
	if rec := request(t, a.Server, "POST", "/api/sessions/nope/messages",
		map[string]any{"text": "hi"}); rec.Code != 404 {
		t.Errorf("message to an unknown session = %d, want 404", rec.Code)
	}
}

func TestRunFailsWhenTheWorkspaceIsNotRunning(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	if rec := request(t, a.Server, "POST", "/api/workspaces/"+sess.WorkspaceID+"/stop", nil); rec.Code != 200 {
		t.Fatalf("stop = %d", rec.Code)
	}
	a.script(providertest.Text("unreachable"))
	a.postMessage(t, sess.ID, "hello", "", 409)
}

func TestRunRecordsAProviderFailure(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(providertest.Step{Err: errNoModel})

	a.postMessage(t, sess.ID, "hello", "", 202)
	state := a.waitIdle(t, sess.ID)
	if state.Run == nil || state.Run.State != "error" {
		t.Fatalf("run = %+v, want an error run", state.Run)
	}
	if !strings.Contains(state.Run.Error, errNoModel.Error()) {
		t.Errorf("error = %q, want the provider's message", state.Run.Error)
	}
}

// errNoModel is the failure the scripted provider reports when a test asks
// for a run that cannot reach a model.
var errNoModel = errors.New("the model endpoint is unreachable")

func TestOnlyOneOfTwoConcurrentRunsStarts(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(providertest.Text("one answer"))

	// Building a run reads the database and runs a command in the workspace.
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

func TestNoRunStartsAfterTheHarnessStops(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.script(providertest.Text("never asked for"))

	a.Server.Close()
	rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "hi"})
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	runs, err := a.store.Runs(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("run rows = %d, want none", len(runs))
	}
}
