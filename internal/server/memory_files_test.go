package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"testing"

	"github.com/erlidev/eika/internal/memory"
)

// staleFiles fails every call, as a repository does once its computer's
// address is gone.
type staleFiles struct{ memory.Files }

func (staleFiles) Read(context.Context, string) ([]byte, error) {
	return nil, errors.New("connection refused")
}

type liveFiles struct{ memory.Files }

func (liveFiles) Read(context.Context, string) ([]byte, error) { return []byte("ok"), nil }

type missingFiles struct{ memory.Files }

func (missingFiles) Read(context.Context, string) ([]byte, error) { return nil, fs.ErrNotExist }

func TestMemoryFilesRefreshAfterFailure(t *testing.T) {
	fresh := 0
	f := &memoryFiles{log: slog.Default(), cur: staleFiles{}, fresh: func(context.Context) (memory.Files, error) {
		fresh++
		return liveFiles{}, nil
	}}
	for range 2 {
		if b, err := f.Read(t.Context(), "x"); err != nil || string(b) != "ok" {
			t.Fatalf("Read = %q, %v", b, err)
		}
	}
	if fresh != 1 {
		t.Errorf("refreshed %d times, want once: the new repository is kept", fresh)
	}

	// A missing file is an answer, not a stale executor.
	f = &memoryFiles{log: slog.Default(), cur: missingFiles{}, fresh: func(context.Context) (memory.Files, error) {
		t.Error("refreshed for a missing file")
		return nil, nil
	}}
	if _, err := f.Read(t.Context(), "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read error = %v", err)
	}

	// When the computer cannot be ensured the original error comes back.
	f = &memoryFiles{log: slog.Default(), cur: staleFiles{}, fresh: func(context.Context) (memory.Files, error) {
		return nil, errors.New("no docker")
	}}
	if _, err := f.Read(t.Context(), "x"); err == nil || err.Error() != "connection refused" {
		t.Errorf("Read error = %v", err)
	}
}
