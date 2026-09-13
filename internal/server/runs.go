package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// The modes a message can be posted in. A run starts a turn; steering and
// follow-up messages join the run that is already going.
const (
	modeRun      = "run"
	modeSteer    = "steer"
	modeFollowUp = "follow_up"
)

// finishTimeout bounds the database write that records how a run ended, which
// happens after the request that started the run is long gone.
const finishTimeout = 30 * time.Second

// messageRequest is the body of POST /api/sessions/{id}/messages.
type messageRequest struct {
	Text string `json:"text"`
	// Mode is run, steer, or follow_up. Empty means run.
	Mode string `json:"mode"`
	// Model overrides the model this run uses. Empty uses the default from
	// the settings, or the first configured model.
	Model string `json:"model"`
}

// runBody is one agent run on the wire.
type runBody struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	State      string    `json:"state"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Error      string    `json:"error,omitempty"`
}

// runStateResponse is the body of GET /api/sessions/{id}/run: what the
// session is doing and what is waiting to be delivered to it.
type runStateResponse struct {
	SessionID string `json:"session_id"`
	// Active reports whether a run is going right now. A run row with
	// Active false is the last run that finished.
	Active bool     `json:"active"`
	Run    *runBody `json:"run,omitempty"`
	// Queued messages, oldest first, that the run has not delivered yet.
	PendingSteering  []string `json:"pending_steering"`
	PendingFollowUps []string `json:"pending_follow_ups"`
	// Questions the run is waiting on an answer for.
	Questions []builtin.Question `json:"questions"`
}

// runs owns the agent runs that are going right now: one goroutine each, one
// run per session at a time. A run outlives the request that started it, so
// it has its own context, cancelled by an abort or by shutdown.
type runs struct {
	server *Server

	mu        sync.Mutex
	bySession map[string]*activeRun
	byID      map[string]*activeRun
}

// activeRun is one execution of the agent loop.
type activeRun struct {
	id          string
	sessionID   string
	workspaceID string
	agent       *agent.Agent
	cancel      context.CancelFunc
	done        chan struct{}

	// aborted separates a run the user stopped from one that failed; both
	// end with a cancelled context.
	mu      sync.Mutex
	aborted bool
}

// newRuns returns an empty run manager for the server.
func newRuns(s *Server) *runs {
	return &runs{
		server:    s,
		bySession: make(map[string]*activeRun),
		byID:      make(map[string]*activeRun),
	}
}

// handlePostMessage delivers a message to a session: starting a run, steering
// the run that is going, or queueing a follow-up for after it.
func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[messageRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		s.fail(w, r, invalidf("text is required"))
		return
	}
	id := r.PathValue("id")
	switch mode := req.Mode; mode {
	case "", modeRun:
		run, err := s.runs.start(r.Context(), id, req.Text, req.Model)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, s.log, http.StatusAccepted, asRun(run))
	case modeSteer, modeFollowUp:
		run, err := s.runs.enqueue(r.Context(), id, req.Text, mode)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, s.log, http.StatusAccepted, asRun(run))
	default:
		s.fail(w, r, invalidf("mode must be %q, %q, or %q", modeRun, modeSteer, modeFollowUp))
	}
}

// handleSessionRun reports what a session is doing and what is queued for it.
func (s *Server) handleSessionRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	body := runStateResponse{
		SessionID:        id,
		PendingSteering:  []string{},
		PendingFollowUps: []string{},
		Questions:        []builtin.Question{},
	}
	if s.deps.Questions != nil {
		for _, q := range s.deps.Questions.Pending() {
			if q.SessionID == id {
				body.Questions = append(body.Questions, q)
			}
		}
	}
	if active := s.runs.active(id); active != nil {
		run, err := s.deps.Store.Run(r.Context(), active.id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		reply := asRun(run)
		body.Active = true
		body.Run = &reply
		body.PendingSteering = append(body.PendingSteering, active.agent.PendingSteering()...)
		body.PendingFollowUps = append(body.PendingFollowUps, active.agent.PendingFollowUps()...)
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}
	previous, err := s.deps.Store.Runs(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(previous) > 0 {
		reply := asRun(previous[len(previous)-1])
		body.Run = &reply
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleAbortRun stops a run and reports how it ended.
func (s *Server) handleAbortRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.abort(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asRun(run))
}

// start begins a run on a session and returns its row. A session that is
// already running one is a conflict: the message belongs in a queue instead.
func (r *runs) start(ctx context.Context, sessionID, text, model string) (store.Run, error) {
	s := r.server
	sess, err := s.deps.Store.Session(ctx, sessionID)
	if err != nil {
		return store.Run{}, err
	}
	r.mu.Lock()
	_, running := r.bySession[sessionID]
	r.mu.Unlock()
	if running {
		return store.Run{}, conflictf("session %s already has a run in progress", sessionID)
	}

	ex, err := s.executorFor(ctx, sess.WorkspaceID)
	if err != nil {
		return store.Run{}, err
	}
	name, err := s.modelName(ctx, model)
	if err != nil {
		return store.Run{}, err
	}
	p, err := s.deps.Models.Provider(name)
	if err != nil {
		return store.Run{}, err
	}
	sessionStore := session.NewStore(s.tree, workspaceCommit(ex))
	loaded, err := sessionStore.Load(ctx, sessionID)
	if err != nil {
		return store.Run{}, err
	}
	// Everything a later phase adds to a run - subagent tools in phase 6,
	// search tools in phase 7 - is registered in this Options value.
	ag := agent.New(p, s.deps.Tools, agent.Options{
		Model:    name,
		Executor: ex,
		Emitter:  s.deps.Bus,
		Store:    sessionStore,
		Logger:   s.log,
	})

	row, err := s.deps.Store.StartRun(ctx, sessionID)
	if err != nil {
		return store.Run{}, err
	}
	// The run is not bound to the request: the client gets its answer as soon
	// as the run starts and follows the rest on the event stream.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	active := &activeRun{
		id:          row.ID,
		sessionID:   sessionID,
		workspaceID: sess.WorkspaceID,
		agent:       ag,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	r.mu.Lock()
	r.bySession[sessionID] = active
	r.byID[row.ID] = active
	r.mu.Unlock()

	go r.drive(runCtx, active, loaded, text)
	s.log.Info("run started", "run_id", row.ID, "session_id", sessionID, "model", name)
	return row, nil
}

// drive runs the agent loop and records how it ended.
func (r *runs) drive(ctx context.Context, active *activeRun, loaded *agent.Session, text string) {
	defer close(active.done)
	defer active.cancel()
	err := active.agent.Run(ctx, loaded, text)

	state, message := store.RunDone, ""
	switch {
	case err != nil && active.wasAborted():
		state = store.RunAborted
	case err != nil:
		state, message = store.RunError, err.Error()
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if err := r.server.deps.Store.FinishRun(finishCtx, active.id, state, message); err != nil {
		r.server.log.Error("record finished run", "run_id", active.id, "error", err)
	}
	r.forget(active)
	r.server.log.Info("run finished", "run_id", active.id, "session_id", active.sessionID, "state", state)
}

// enqueue delivers a steering or follow-up message to the run in progress.
func (r *runs) enqueue(ctx context.Context, sessionID, text, mode string) (store.Run, error) {
	active := r.active(sessionID)
	if active == nil {
		if _, err := r.server.deps.Store.Session(ctx, sessionID); err != nil {
			return store.Run{}, err
		}
		return store.Run{}, conflictf("session %s has no run in progress to %s", sessionID, mode)
	}
	if mode == modeSteer {
		active.agent.Steer(text)
	} else {
		active.agent.FollowUp(text)
	}
	return r.server.deps.Store.Run(ctx, active.id)
}

// abort stops a run and waits for its goroutine to finish, so that the row it
// returns is the final one.
func (r *runs) abort(ctx context.Context, runID string) (store.Run, error) {
	r.mu.Lock()
	active, ok := r.byID[runID]
	r.mu.Unlock()
	if !ok {
		row, err := r.server.deps.Store.Run(ctx, runID)
		if err != nil {
			return store.Run{}, err
		}
		if row.State == store.RunRunning {
			// A row left running by a harness restart has no goroutine to
			// stop; record that it is over.
			if err := r.server.deps.Store.FinishRun(ctx, runID, store.RunAborted, ""); err != nil {
				return store.Run{}, err
			}
			return r.server.deps.Store.Run(ctx, runID)
		}
		return store.Run{}, conflictf("run %s is already %s", runID, row.State)
	}
	active.abort()
	select {
	case <-active.done:
	case <-ctx.Done():
		return store.Run{}, ctx.Err()
	}
	return r.server.deps.Store.Run(ctx, runID)
}

// stop aborts the run of one session, if it has one, and waits for it.
func (r *runs) stop(ctx context.Context, sessionID string) {
	active := r.active(sessionID)
	if active == nil {
		return
	}
	active.abort()
	select {
	case <-active.done:
	case <-ctx.Done():
	}
}

// stopSessionsOf aborts every run in a workspace, which is what stopping or
// deleting that workspace has to do first: the runs reach it through an
// executor that is about to go away.
func (r *runs) stopSessionsOf(ctx context.Context, st *store.Store, workspaceID string) {
	sessions, err := st.Sessions(ctx, workspaceID)
	if err != nil {
		r.server.log.Error("list sessions of workspace", "workspace_id", workspaceID, "error", err)
		return
	}
	for _, sess := range sessions {
		r.stop(ctx, sess.ID)
	}
}

// stopAll aborts every run, which is what a shutting down harness does before
// its database pool closes.
func (r *runs) stopAll() {
	r.mu.Lock()
	active := make([]*activeRun, 0, len(r.byID))
	for _, a := range r.byID {
		active = append(active, a)
	}
	r.mu.Unlock()
	for _, a := range active {
		a.abort()
		<-a.done
	}
}

// active returns the run of a session, or nil when it has none.
func (r *runs) active(sessionID string) *activeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bySession[sessionID]
}

// forget drops a finished run.
func (r *runs) forget(a *activeRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.bySession[a.sessionID]; ok && current == a {
		delete(r.bySession, a.sessionID)
	}
	delete(r.byID, a.id)
}

// abort cancels the run and records that the stop was asked for.
func (a *activeRun) abort() {
	a.mu.Lock()
	a.aborted = true
	a.mu.Unlock()
	a.cancel()
}

// wasAborted reports whether the run was stopped on purpose.
func (a *activeRun) wasAborted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.aborted
}

// modelName resolves which model a run uses: the one the request named, the
// one the settings default to, or the first configured model.
func (s *Server) modelName(ctx context.Context, requested string) (string, error) {
	names := s.deps.Models.Names()
	if len(names) == 0 {
		return "", conflictf("no model is configured")
	}
	if requested != "" {
		for _, n := range names {
			if n == requested {
				return n, nil
			}
		}
		return "", invalidf("unknown model %q", requested)
	}
	if def := s.defaultModel(ctx); def != "" {
		for _, n := range names {
			if n == def {
				return n, nil
			}
		}
		s.log.Warn("configured default model is unknown", "model", def)
	}
	return names[0], nil
}

// workspaceCommit reports the workspace's HEAD commit for the entries a run
// writes. A workspace that holds no repository yet reports no commit rather
// than failing the run.
func workspaceCommit(ex executor.Executor) session.CommitFunc {
	return func(ctx context.Context, _ string) (string, error) {
		var out bytes.Buffer
		res, err := ex.Exec(ctx, executor.ExecSpec{
			Command: "git",
			Args:    []string{"rev-parse", "HEAD"},
			Timeout: gitTimeout,
			Stdout:  &out,
			Stderr:  io.Discard,
		})
		if err != nil || res.ExitCode != 0 {
			return "", nil
		}
		return strings.TrimSpace(out.String()), nil
	}
}

// asRun renders a stored run on the wire.
func asRun(r store.Run) runBody {
	return runBody{
		ID:         r.ID,
		SessionID:  r.SessionID,
		State:      string(r.State),
		StartedAt:  r.StartedAt,
		FinishedAt: r.FinishedAt,
		Error:      r.Error,
	}
}
