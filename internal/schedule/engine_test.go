package schedule_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/schedule"
)

type fakeClaims struct {
	mu                              sync.Mutex
	routine                         bool
	wakeup                          bool
	routineFinished, wakeupFinished chan struct{}
	at                              time.Time
}

func (f *fakeClaims) ClaimRoutine(_ context.Context, now time.Time) (schedule.Routine, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = now
	if f.routine {
		return schedule.Routine{}, false, nil
	}
	f.routine = true
	return schedule.Routine{RunID: "run", MaxRuntime: time.Minute}, true, nil
}
func (f *fakeClaims) ClaimWakeup(_ context.Context, _ time.Time) (schedule.Wakeup, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeup {
		return schedule.Wakeup{}, false, nil
	}
	f.wakeup = true
	return schedule.Wakeup{ID: "wake"}, true, nil
}
func (f *fakeClaims) FinishRoutine(_ context.Context, _, _, _, _ string, _ time.Time) error {
	close(f.routineFinished)
	return nil
}
func (f *fakeClaims) FinishWakeup(_ context.Context, _ string, done bool) error {
	if done {
		close(f.wakeupFinished)
	}
	return nil
}

type fakeRunner struct{}

func (fakeRunner) RunRoutine(_ context.Context, _ schedule.Routine) (string, string, string) {
	return "session", "done", ""
}
func (fakeRunner) RunWakeup(_ context.Context, _ schedule.Wakeup) error { return nil }
func TestEngineClaimsAndFinishes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	claims := &fakeClaims{routineFinished: make(chan struct{}), wakeupFinished: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		(schedule.Engine{Store: claims, Runner: fakeRunner{}, Now: func() time.Time { return now }, Interval: time.Hour}).Run(ctx)
		close(done)
	}()
	for _, ch := range []<-chan struct{}{claims.routineFinished, claims.wakeupFinished} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("claim did not finish")
		}
	}
	cancel()
	<-done
	claims.mu.Lock()
	defer claims.mu.Unlock()
	if !claims.at.Equal(now) {
		t.Fatal("clock not used")
	}
}
