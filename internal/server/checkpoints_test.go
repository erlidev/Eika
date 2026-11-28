package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/store"
)

type failingCheckpoints struct {
	*local.Executor
	asked int
}

func (f *failingCheckpoints) Checkpoint(context.Context, eikad.CheckpointRequest) (eikad.CheckpointResponse, error) {
	f.asked++
	return eikad.CheckpointResponse{}, errors.New("git is not installed")
}

func TestAFailedCheckpointIsLoggedAndNothingElse(t *testing.T) {
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if s.projectCheckpointer(store.Session{}, workdir{Executor: ex, dir: "projects/app"}) != nil {
		t.Error("an executor that is not a sandbox got a checkpointer")
	}
	client := &failingCheckpoints{Executor: ex}
	take := s.projectCheckpointer(store.Session{}, workdir{Executor: client, dir: "projects/app"})
	if take == nil {
		t.Fatal("a sandbox client in a project got no checkpointer")
	}
	// The server has no store here: a failure must return before one is used.
	take(t.Context(), "run1", eikad.CheckpointStart)
	if client.asked != 1 {
		t.Errorf("checkpoint asked %d times, want 1", client.asked)
	}
}
