package server

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/store"
)

// checkpointTimeout bounds one checkpoint, which must never hold a run up for
// long.
const checkpointTimeout = 2 * time.Minute

// checkpointClient is what a sandbox's client offers beyond the executor: the
// daemon's checkpoint route. It is kept off the executor so that no tool can
// reach it.
type checkpointClient interface {
	Checkpoint(ctx context.Context, req eikad.CheckpointRequest) (eikad.CheckpointResponse, error)
}

// checkpointer takes a run's checkpoints of its project tree.
type checkpointer func(ctx context.Context, runID, label string)

// projectCheckpointer returns the checkpointer of a run working in a project
// directory of a sandbox, and nil for any other run. ex is the project's
// workdir executor.
func (s *Server) projectCheckpointer(sess store.Session, ex executor.Executor) checkpointer {
	w, ok := ex.(workdir)
	if !ok {
		return nil
	}
	client, ok := w.Executor.(checkpointClient)
	if !ok {
		return nil
	}
	return func(ctx context.Context, runID, label string) {
		ctx, cancel := context.WithTimeout(ctx, checkpointTimeout)
		defer cancel()
		resp, err := client.Checkpoint(ctx, eikad.CheckpointRequest{Dir: w.dir, Run: runID, Label: label})
		if err != nil {
			// A checkpoint is a safety net: failing to take one never fails
			// the run.
			s.log.Warn("checkpoint failed", "run_id", runID, "session_id", sess.ID, "label", label, "dir", w.dir, "error", err)
			return
		}
		s.log.Info("checkpoint taken", "run_id", runID, "label", label, "dir", w.dir,
			"files", resp.Files, "skipped", resp.Skipped, "took_ms", resp.TookMS)
		err = s.deps.Store.AddCheckpoint(ctx, store.Checkpoint{
			SessionID: sess.ID, RunID: runID, ComputerID: sess.ComputerID, Dir: w.dir, Label: label,
			Commit: resp.Commit, Files: resp.Files, Skipped: resp.Skipped,
			Took: time.Duration(resp.TookMS) * time.Millisecond,
		})
		if err != nil {
			s.log.Warn("record checkpoint failed", "run_id", runID, "label", label, "error", err)
		}
	}
}
