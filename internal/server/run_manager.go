package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// finishTimeout bounds the database write that records how a run ended, which
// happens after the request that started the run is long gone.
const finishTimeout = 30 * time.Second

const finishRetryBackoff = 100 * time.Millisecond

// job is what a run does: answer a message, or compact the conversation and
// stop.
type job struct {
	// message is the user message the run answers.
	message provider.Message
	// compact makes the run a compaction, which instructions focus.
	compact      bool
	instructions string
}

type queuedMessages struct {
	steering  []provider.Message
	followUps []provider.Message
}

// finishRetries owns final run-state writes that outlive their foreground
// retry window. It cancels and joins every retry before the store closes.
type finishRetries struct {
	wg sync.WaitGroup

	mu      sync.Mutex
	stopped bool
	nextID  uint64
	cancels map[uint64]context.CancelFunc
}

func newFinishRetries() *finishRetries {
	return &finishRetries{cancels: make(map[uint64]context.CancelFunc)}
}

// run retries in the foreground first. If that context ends, it continues
// the same idempotent write in an owned background goroutine.
func (r *finishRetries) run(ctx context.Context, backoff time.Duration, finish func(context.Context) error, complete func(error)) error {
	err := retryFinish(ctx, backoff, finish)
	if err == nil {
		return nil
	}
	r.start(backoff, finish, complete)
	return err
}

func (r *finishRetries) start(backoff time.Duration, finish func(context.Context) error, complete func(error)) bool {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		cancel()
		return false
	}
	id := r.nextID
	r.nextID++
	r.cancels[id] = cancel
	r.wg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		err := retryFinish(ctx, backoff, finish)
		if complete != nil {
			complete(err)
		}
		cancel()
		r.mu.Lock()
		delete(r.cancels, id)
		r.mu.Unlock()
	}()
	return true
}

func (r *finishRetries) stop() {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		for _, cancel := range r.cancels {
			cancel()
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// runs owns the agent runs that are going right now: one goroutine each, one
// run per session at a time. A run outlives the request that started it, so
// it has its own context, cancelled by an abort or by shutdown.
type runs struct {
	server   *Server
	finishes *finishRetries

	mu        sync.Mutex
	stopped   bool
	bySession map[string]*activeRun
	byID      map[string]*activeRun
	// pending keeps accepted messages from a run that stopped before it
	// delivered them. The next run on the session receives them.
	pending map[string]queuedMessages
}

// activeRun is one execution of the agent loop, from the moment its session
// is claimed. Building a run takes a database read and a command in the
// computer, so the claim exists before the run row and the loop do; runID
// and loop report empty until they are there.
type activeRun struct {
	sessionID  string
	computerID string
	borrowed   map[string]int
	cancel     context.CancelFunc
	done       chan struct{}
	// releaseCounter ends the run's use of its session's token counter routes,
	// which carry the provider's key. Set before the run starts and called on
	// every way out of it; nil until the run has a counter.
	releaseCounter func()

	mu    sync.Mutex
	id    string
	agent *agent.Agent
	// model is the run's model, whose image input decides whether a
	// steering or follow-up message may carry images.
	model store.Model
	// aborted separates a run the user stopped from one that failed; both
	// end with a cancelled context.
	aborted bool
}

// newRuns returns an empty run manager for the server.
func newRuns(s *Server) *runs {
	return &runs{
		server:    s,
		bySession: make(map[string]*activeRun),
		byID:      make(map[string]*activeRun),
		pending:   make(map[string]queuedMessages),
		finishes:  newFinishRetries(),
	}
}

// start begins a run on a session and returns its row. A session that is
// already running one is a conflict: the message belongs in a queue instead.
func (r *runs) start(ctx context.Context, sessionID string, message provider.Message, model string) (store.Run, error) {
	return r.begin(ctx, sessionID, model, job{message: message})
}

// compact begins a run that compacts a session's conversation and does
// nothing else. A conversation with nothing old enough to summarize is
// agent.ErrNothingToCompact, and no run starts.
func (r *runs) compact(ctx context.Context, sessionID, instructions, model string) (store.Run, error) {
	return r.begin(ctx, sessionID, model, job{compact: true, instructions: instructions})
}

// begin builds a run and starts its goroutine. The run outlives the request
// that asked for it and is stopped by an abort alone.
func (r *runs) begin(ctx context.Context, sessionID, model string, work job) (store.Run, error) {
	s := r.server
	sess, err := s.deps.Store.Session(ctx, sessionID)
	if err != nil {
		return store.Run{}, err
	}

	// The run is not bound to the request: the client gets its answer as
	// soon as the run starts and follows the rest on the event stream.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.startMu.Lock()
	active, err := r.reserve(sessionID, sess.ComputerID, cancel)
	s.startMu.Unlock()
	if err != nil {
		cancel()
		return store.Run{}, err
	}
	// Building a run reads the database and runs a command in the computer.
	// Until that is done and the loop is running, the reservation is what
	// holds the session, and every way out of here releases it.
	started := false
	defer func() {
		if !started {
			r.abandon(active)
		}
	}()

	cfg, err := s.configure(ctx, sess, model)
	if err != nil {
		return store.Run{}, err
	}
	var ex executor.Executor
	if s.usesComputer(sess) {
		if ex, err = s.runExecutor(ctx, sess.ComputerID); err != nil {
			return store.Run{}, err
		}
	}
	if len(work.message.Images) > 0 && !cfg.model.ImageInput {
		return store.Run{}, noImageInput(cfg.model.Name)
	}
	ep, err := s.endpointFor(ctx, cfg)
	if err != nil {
		return store.Run{}, err
	}
	cfg = cfg.withProbe(ep.row)
	// The counter keeps what it learned from run to run; its routes, which
	// carry the key, are attached until this run ends, whichever way.
	counted, releaseRoutes := s.counters.forRun(sessionID, ep, cfg.model)
	active.releaseCounter = releaseRoutes
	sessionStore := session.NewStore(s.tree)
	if sess.Kind == store.SessionHome {
		sessionStore.SetChannel("web")
	}
	loaded, err := sessionStore.Load(ctx, sessionID)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.ensurePreamble(ctx, sess, loaded); err != nil {
		return store.Run{}, err
	}
	var refresh func(context.Context, string, agent.Compaction) (string, error)
	if sess.Agent != "" {
		refresh = s.refreshPreamble
	}
	// The tools every run shares hold what they reach beyond the computer:
	// the search engine and the page reader. A run narrows
	// them to its tool choice, together with the tools of the MCP servers it
	// reaches now, and adds only its own executor, which is also where a
	// web_fetch filter runs.
	ag := s.newAgent(ep.client, sess, cfg, ex, s.mcpToolsFor(ctx, sess, cfg.tools), agent.Options{
		RefreshPreamble: refresh,
		Location:        agent.PreambleLocation(loaded.Preamble),
		Emitter:         s.deps.Bus,
		Store:           sessionStore,
		Logger:          s.log,
		Recorder:        callRecorder{store: s.deps.Store, run: active, config: cfg, endpoint: ep.row.Name},
		Counter:         counted,
		Compaction:      s.compaction(ctx, cfg.model),
		// A run, and the compaction a user asks for, answers a person who is
		// waiting; a run that is not interactive says so by raising or
		// lowering this rank, as the home session will.
		Inference: agent.Inference{
			Queue:    s.queue(ctx),
			Pool:     ep.row.PoolName(),
			Endpoint: ep.row.Name,
			Rank:     inference.NewRank(inference.Interactive),
			// The speed the endpoint has read prompts at, which says how
			// long a call may be silent before its first token.
			PrefillRate: s.prefillRate(ctx, ep.row.Name),
		},
	})
	if work.compact && !ag.Compactable(loaded) {
		return store.Run{}, conflictf("%v", agent.ErrNothingToCompact)
	}

	row, err := s.deps.Store.StartRun(ctx, sessionID)
	if err != nil {
		return store.Run{}, err
	}
	// A compaction delivers no messages, so what is queued waits for the
	// next run that does.
	if !work.compact {
		queued := r.takePending(sessionID)
		for _, message := range queued.steering {
			ag.Steer(message)
		}
		for _, message := range queued.followUps {
			ag.FollowUp(message)
		}
	}
	active.begin(row.ID, ag, cfg.model)
	r.mu.Lock()
	r.byID[row.ID] = active
	r.mu.Unlock()

	started = true
	// The title comes from the conversation as it stood before the run, so
	// it is read before the loop starts adding to it.
	if sess.Untitled && !work.compact {
		if first := firstMessage(loaded.Conversation.Messages(), work.message.Content); first != "" {
			s.titles.start(sess, first)
		}
	}
	go r.drive(runCtx, active, loaded, work)
	s.log.Info("run started", "run_id", row.ID, "session_id", sessionID, "model", cfg.model.Name,
		"profile_id", cfg.profile.ID)
	return row, nil
}

// reserve claims a session for a run that is about to start. Claiming under
// the lock that reads the check is what makes one run per session true:
// building a run takes long enough that two requests would otherwise both get
// past a check that only looked at the runs already going.
func (r *runs) reserve(sessionID, computerID string, cancel context.CancelFunc) (*activeRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, conflictf("the harness is shutting down")
	}
	if _, running := r.bySession[sessionID]; running {
		return nil, conflictf("session %s already has a run in progress", sessionID)
	}
	active := &activeRun{
		sessionID:  sessionID,
		computerID: computerID,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	r.bySession[sessionID] = active
	return active, nil
}

// abandon releases a reservation whose run never began, which frees the
// session and releases whoever is waiting on it.
func (r *runs) abandon(active *activeRun) {
	active.releaseCounterRoutes()
	r.forget(active)
	active.cancel()
	close(active.done)
}

// drive runs the agent loop, or the compaction the run is, and records how it
// ended.
func (r *runs) drive(ctx context.Context, active *activeRun, loaded *agent.Session, work job) {
	defer active.releaseCounterRoutes()
	defer close(active.done)
	defer active.cancel()
	runID := active.runID()
	loop := active.loop()
	var err error
	if work.compact {
		err = loop.Compact(ctx, loaded, work.instructions)
	} else {
		err = loop.Run(ctx, loaded, work.message)
	}

	state, message := store.RunDone, ""
	switch {
	case err != nil && active.wasAborted():
		state = store.RunAborted
	case err != nil:
		state, message = store.RunError, err.Error()
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	finish := func(ctx context.Context) error {
		return r.server.deps.Store.FinishRun(ctx, runID, state, message)
	}
	if err := r.finishes.run(finishCtx, finishRetryBackoff, finish, func(err error) {
		if err != nil {
			r.server.log.Error("record finished run in background", "run_id", runID, "error", err)
			return
		}
		r.server.log.Info("finished run state recorded in background", "run_id", runID, "state", state)
	}); err != nil {
		r.server.log.Warn("record finished run in foreground; continuing in background", "run_id", runID, "error", err)
	}
	r.finish(active, queuedMessages{
		steering:  loop.PendingSteering(),
		followUps: loop.PendingFollowUps(),
	})
	r.server.log.Info("run finished", "run_id", runID, "session_id", active.sessionID, "state", state)
}

// enqueue delivers a steering or follow-up message to the run in progress. A
// run that is still being built has no queue to take it yet, so it counts as
// no run: the client retries once the run it started reports itself.
func (r *runs) enqueue(ctx context.Context, sessionID string, message provider.Message, mode string) (store.Run, error) {
	active := r.active(sessionID)
	var loop *agent.Agent
	if active != nil {
		loop = active.loop()
	}
	if loop == nil {
		if _, err := r.server.deps.Store.Session(ctx, sessionID); err != nil {
			return store.Run{}, err
		}
		return store.Run{}, conflictf("session %s has no run in progress to %s", sessionID, mode)
	}
	if model := active.runModel(); len(message.Images) > 0 && !model.ImageInput {
		return store.Run{}, noImageInput(model.Name)
	}
	row, err := r.server.deps.Store.Run(ctx, active.runID())
	if err != nil {
		return store.Run{}, err
	}
	accepted := false
	if mode == modeSteer {
		accepted = loop.Steer(message)
	} else {
		accepted = loop.FollowUp(message)
	}
	if !accepted {
		return store.Run{}, conflictf("session %s has no run in progress to %s", sessionID, mode)
	}
	return row, nil
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

// stopSessionsOf aborts every run in a computer, which is what stopping or
// deleting that computer has to do first: the runs reach it through an
// executor that is about to go away.
func (r *runs) stopSessionsOf(ctx context.Context, st *store.Store, computerID string) {
	sessions, err := st.Sessions(ctx, computerID)
	if err != nil {
		r.server.log.Error("list sessions of computer", "computer_id", computerID, "error", err)
		return
	}
	for _, sess := range sessions {
		r.stop(ctx, sess.ID)
	}
}

// stopAll aborts every run, which is what a shutting down harness does before
// its database pool closes. No run starts after it: the session a late
// request claims would outlive the pool it writes to.
func (r *runs) stopAll() {
	r.mu.Lock()
	r.stopped = true
	active := make([]*activeRun, 0, len(r.bySession))
	for _, a := range r.bySession {
		active = append(active, a)
	}
	r.mu.Unlock()
	for _, a := range active {
		a.abort()
		<-a.done
	}
	r.finishes.stop()
}

// active returns the run of a session, or nil when it has none.
func (r *runs) active(sessionID string) *activeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bySession[sessionID]
}

// computersInUse reports the computers that have a run going, or one being
// built, which the idle stopper leaves running.
func (r *runs) computersInUse() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	inUse := make(map[string]bool, len(r.bySession))
	for _, a := range r.bySession {
		if a.computerID != "" {
			inUse[a.computerID] = true
		}
		for id := range a.borrowed {
			inUse[id] = true
		}
	}
	return inUse
}

// borrowComputer adds a computer to an active run until the caller releases it.
func (r *runs) borrowComputer(sessionID, computerID string) (func(), error) {
	r.mu.Lock()
	a := r.bySession[sessionID]
	if a == nil {
		r.mu.Unlock()
		return nil, conflictf("session %s has no active run", sessionID)
	}
	if a.borrowed == nil {
		a.borrowed = make(map[string]int)
	}
	a.borrowed[computerID]++
	r.mu.Unlock()
	return func() {
		r.server.idle.used(computerID, time.Now())
		r.mu.Lock()
		a.borrowed[computerID]--
		if a.borrowed[computerID] == 0 {
			delete(a.borrowed, computerID)
		}
		r.mu.Unlock()
	}, nil
}

// forget drops a finished run.
func (r *runs) forget(a *activeRun) {
	r.server.idle.used(a.computerID, time.Now())
	id := a.runID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.bySession[a.sessionID]; ok && current == a {
		delete(r.bySession, a.sessionID)
	}
	if id != "" {
		delete(r.byID, id)
	}
}

// finish keeps undelivered accepted messages for the next run and drops the
// active run under one lock.
func (r *runs) finish(a *activeRun, queued queuedMessages) {
	// A run shorter than the idle stopper's sweep is never seen going, so
	// its end is what counts as the computer's last use.
	r.server.idle.used(a.computerID, time.Now())
	id := a.runID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(queued.steering) != 0 || len(queued.followUps) != 0 {
		r.pending[a.sessionID] = queued
	}
	if current, ok := r.bySession[a.sessionID]; ok && current == a {
		delete(r.bySession, a.sessionID)
	}
	delete(r.byID, id)
}

// pendingFor returns accepted messages that wait for the next run.
func (r *runs) pendingFor(sessionID string) queuedMessages {
	r.mu.Lock()
	defer r.mu.Unlock()
	queued := r.pending[sessionID]
	queued.steering = append([]provider.Message(nil), queued.steering...)
	queued.followUps = append([]provider.Message(nil), queued.followUps...)
	return queued
}

// takePending transfers accepted messages to a new run.
func (r *runs) takePending(sessionID string) queuedMessages {
	r.mu.Lock()
	defer r.mu.Unlock()
	queued := r.pending[sessionID]
	delete(r.pending, sessionID)
	return queued
}

// retryFinish retries a final run-state write until it succeeds or its
// bounded context ends. The write is idempotent.
func retryFinish(ctx context.Context, backoff time.Duration, finish func(context.Context) error) error {
	var err error
	for {
		if err = finish(ctx); err == nil {
			return nil
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		}
	}
}

// releaseCounterRoutes ends the run's use of its counter's routes, if it has
// any. It is safe to call more than once.
func (a *activeRun) releaseCounterRoutes() {
	if a.releaseCounter != nil {
		a.releaseCounter()
	}
}

// begin records the run row, the loop, and its model on a reservation, which
// is what turns it into a run the other handlers can report on and steer.
func (a *activeRun) begin(id string, loop *agent.Agent, model store.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.id, a.agent, a.model = id, loop, model
}

// runModel returns the model of the run, the zero model while it is still
// being built.
func (a *activeRun) runModel() store.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}

// runID returns the id of the run's row, empty while it is still being built.
func (a *activeRun) runID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.id
}

// loop returns the agent driving the run, nil while it is still being built.
func (a *activeRun) loop() *agent.Agent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.agent
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
