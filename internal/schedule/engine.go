package schedule

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Routine is a claimed routine and its new run.
type Routine struct {
	ID, RunID, Name, Agent, ProjectID, Prompt string
	MaxRuntime                                time.Duration
}

// Wakeup is a claimed request to resume a session.
type Wakeup struct {
	ID, SessionID, Agent, Note string
	DueAt                      time.Time
}

// Claims stores scheduler claims and their outcomes.
type Claims interface {
	ClaimRoutine(context.Context, time.Time) (Routine, bool, error)
	ClaimWakeup(context.Context, time.Time) (Wakeup, bool, error)
	FinishRoutine(context.Context, string, string, string, string, time.Time) error
	FinishWakeup(context.Context, string, bool) error
}

// Runner executes a claim and returns the routine session and outcome.
type Runner interface {
	RunRoutine(context.Context, Routine) (sessionID, outcome, reason string)
	RunWakeup(context.Context, Wakeup) error
}

// Engine owns scheduled work until Run returns.
type Engine struct {
	Store    Claims
	Runner   Runner
	Now      func() time.Time
	Interval time.Duration
	Log      *slog.Logger
}

// Run polls for due work until ctx ends, then waits for active work to stop.
func (e Engine) Run(ctx context.Context) {
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Interval <= 0 {
		e.Interval = 15 * time.Second
	}
	if e.Log == nil {
		e.Log = slog.Default()
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	tick := func() {
		claimsDone := make(chan struct{})
		defer close(claimsDone)
		now := e.Now().UTC()
		for ctx.Err() == nil {
			r, ok, err := e.Store.ClaimRoutine(ctx, now)
			if err != nil {
				e.Log.Error("claim routine", "error", err)
				break
			}
			if !ok {
				break
			}
			wg.Go(func() {
				runCtx, cancel := context.WithTimeout(ctx, r.MaxRuntime)
				defer cancel()
				sessionID, outcome, reason := e.Runner.RunRoutine(runCtx, r)
				finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer finishCancel()
				if err := e.Store.FinishRoutine(finishCtx, r.RunID, sessionID, outcome, reason, e.Now().UTC()); err != nil {
					e.Log.Error("finish routine", "run_id", r.RunID, "error", err)
				}
			})
		}
		for ctx.Err() == nil {
			w, ok, err := e.Store.ClaimWakeup(ctx, now)
			if err != nil {
				e.Log.Error("claim wakeup", "error", err)
				break
			}
			if !ok {
				break
			}
			wg.Go(func() {
				err := e.Runner.RunWakeup(ctx, w)
				<-claimsDone
				finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				if finishErr := e.Store.FinishWakeup(finishCtx, w.ID, err == nil); finishErr != nil {
					e.Log.Error("finish wakeup", "wakeup_id", w.ID, "error", finishErr)
				}
			})
		}
	}
	tick()
	timer := time.NewTicker(e.Interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick()
		}
	}
}
