package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryFinishRecoversFromATransientStoreFailure(t *testing.T) {
	attempts := 0
	err := retryFinish(t.Context(), time.Microsecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("database is temporarily unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryFinish: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRetryFinishStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := retryFinish(ctx, time.Hour, func(context.Context) error {
		return errors.New("database is unavailable")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryFinish error = %v, want context.Canceled", err)
	}
}

func TestFinishRetryContinuesAfterTheForegroundContextEnds(t *testing.T) {
	retries := newFinishRetries()
	defer retries.stop()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var attempts atomic.Int32
	done := make(chan error, 1)
	err := retries.run(ctx, time.Microsecond, func(context.Context) error {
		if attempts.Add(1) < 3 {
			return errors.New("database is unavailable")
		}
		return nil
	}, func(err error) {
		done <- err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("foreground retry error = %v, want context.Canceled", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("background retry: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("background retry did not finish")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3 across both retry contexts", got)
	}
}

func TestFinishRetryStopsWithTheRunManager(t *testing.T) {
	retries := newFinishRetries()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	var backgroundStarted atomic.Bool
	err := retries.run(ctx, time.Hour, func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if backgroundStarted.CompareAndSwap(false, true) {
			close(started)
		}
		<-ctx.Done()
		return ctx.Err()
	}, func(err error) {
		done <- err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("foreground retry error = %v, want context.Canceled", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background retry did not start")
	}
	retries.stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("background retry error = %v, want context.Canceled", err)
	}
}
