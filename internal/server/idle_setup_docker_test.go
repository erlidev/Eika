//go:build docker

package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
)

func TestIdleSetupStopsOnShutdown(t *testing.T) {
	url := storetest.URL(t)
	st, err := store.Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	home, err := st.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "LOCK TABLE core.session_entries IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	s := New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Store: st}, Options{})
	defer func() {
		_ = tx.Rollback(context.Background())
		s.Close()
	}()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, _, err := s.runs.launch(ctx, home.ID, "", job{idle: true, compact: true, idleFill: 60, priority: inference.Maintenance})
		done <- err
	}()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := tx.Exec(t.Context(), `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatal(err)
		}
		var blocked bool
		if err := tx.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%session_entries%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked && s.runs.active(home.ID) != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("idle setup did not block on the database")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled setup succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle setup did not stop on shutdown")
	}
}

// TestIdleDriveReleasesBeforeFinalWrite holds the run row while drive finishes.
func TestIdleDriveReleasesBeforeFinalWrite(t *testing.T) {
	url := storetest.URL(t)
	st, err := store.Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	home, err := st.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.StartRun(t.Context(), home.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Store: st}, Options{})
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()); s.Close() }()
	if _, err := tx.Exec(t.Context(), "SELECT id FROM core.runs WHERE id = $1 FOR UPDATE", row.ID); err != nil {
		t.Fatal(err)
	}
	active, err := s.runs.reserve(home.ID, "", job{idle: true, priority: inference.Maintenance}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	loop := agent.New(providertest.New(), nil, agent.Options{})
	active.begin(row.ID, loop, store.Model{}, inference.NewRank(inference.Maintenance))
	s.runs.mu.Lock()
	s.runs.byID[row.ID] = active
	s.runs.mu.Unlock()
	go s.runs.drive(context.Background(), active, agent.NewSession(home.ID, ""), job{idle: true, compact: true})
	deadline := time.After(5 * time.Second)
	for {
		if _, err := tx.Exec(t.Context(), "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		var blocked bool
		if err := tx.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'UPDATE runs SET state%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-deadline:
			t.Fatal("final run write did not block")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	select {
	case <-active.done:
		t.Fatal("idle run finished while its final write was blocked")
	default:
	}
	next, err := s.runs.reserve(home.ID, "", job{}, func() {})
	if err != nil {
		t.Fatalf("session was not released before final write: %v", err)
	}
	s.runs.abandon(next)
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle run did not finish after the lock was released")
	}
}
