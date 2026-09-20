package search_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/search"
)

// memoryUsage is a UsageStore in a map, which can be made to fail.
type memoryUsage struct {
	mu    sync.Mutex
	saved map[string]search.Usage
	err   error
}

func (m *memoryUsage) LoadSearchUsage(context.Context) (map[string]search.Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]search.Usage, len(m.saved))
	for k, v := range m.saved {
		out[k] = v
	}
	return out, m.err
}

func (m *memoryUsage) SaveSearchUsage(_ context.Context, name string, u search.Usage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.saved == nil {
		m.saved = make(map[string]search.Usage)
	}
	m.saved[name] = u
	return nil
}

func newTracker(t *testing.T, store search.UsageStore) *search.Tracker {
	t.Helper()
	tr, err := search.NewTracker(t.Context(), store, discard())
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTrackerRollsCountersOverByUTCDayAndMonth(t *testing.T) {
	tr := newTracker(t, nil)
	jan := time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC)
	tr.RecordUse(t.Context(), "brave", jan)
	if u := tr.Usage("brave", jan); u.DayUsed != 1 || u.MonthUsed != 1 {
		t.Fatalf("usage = %+v, want 1 today and 1 this month", u)
	}
	if u := tr.Usage("brave", jan.Add(2*time.Hour)); u.DayUsed != 0 || u.MonthUsed != 0 {
		t.Errorf("usage on the next day, in the next month = %+v, want zeros", u)
	}
}

func TestTrackerBlocksOnSpentQuota(t *testing.T) {
	tr := newTracker(t, nil)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	tr.RecordUse(t.Context(), "marginalia", now)
	if got := tr.Blocked("marginalia", search.Limit{Day: 1}, now); got != "daily quota spent" {
		t.Errorf("Blocked = %q, want daily quota spent", got)
	}
	if got := tr.Blocked("marginalia", search.Limit{Month: 1}, now); got != "monthly quota spent" {
		t.Errorf("Blocked = %q, want monthly quota spent", got)
	}
	if got := tr.Blocked("marginalia", search.Limit{}, now); got != "" {
		t.Errorf("Blocked with no limit = %q", got)
	}
}

func TestTrackerCooldownBacksOffExponentiallyAndExpires(t *testing.T) {
	tr := newTracker(t, nil)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	tr.RecordFailure(t.Context(), "brave", now, time.Time{})
	if d := tr.Usage("brave", now).CooldownUntil.Sub(now); d != 15*time.Minute {
		t.Errorf("first cooldown = %s, want 15m", d)
	}
	if got := tr.Blocked("brave", search.Limit{}, now); got != "cooling down 15m" {
		t.Errorf("Blocked = %q, want cooling down 15m", got)
	}
	tr.RecordFailure(t.Context(), "brave", now, time.Time{})
	if d := tr.Usage("brave", now).CooldownUntil.Sub(now); d != 30*time.Minute {
		t.Errorf("second cooldown = %s, want 30m", d)
	}
	if got := tr.Blocked("brave", search.Limit{}, now.Add(31*time.Minute)); got != "" {
		t.Errorf("Blocked after the cooldown = %q", got)
	}
}

func TestTrackerCooldownIsCappedAndClearedBySuccess(t *testing.T) {
	tr := newTracker(t, nil)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	for range 20 {
		tr.RecordFailure(t.Context(), "brave", now, time.Time{})
	}
	if d := tr.Usage("brave", now).CooldownUntil.Sub(now); d != 6*time.Hour {
		t.Errorf("cooldown after 20 failures = %s, want the 6h cap", d)
	}
	tr.RecordUse(t.Context(), "brave", now)
	if u := tr.Usage("brave", now); !u.CooldownUntil.IsZero() || u.FailStreak != 0 {
		t.Errorf("usage after a success = %+v, want no cooldown and no streak", u)
	}
}

func TestTrackerHonoursALaterServerDeadline(t *testing.T) {
	tr := newTracker(t, nil)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	reset := now.Add(4 * time.Hour)
	tr.RecordFailure(t.Context(), "github", now, reset)
	if got := tr.Usage("github", now).CooldownUntil; !got.Equal(reset) {
		t.Errorf("cooldown until %s, want the server's %s", got, reset)
	}
}

func TestTrackerPersistsThroughItsStore(t *testing.T) {
	store := &memoryUsage{}
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	tr := newTracker(t, store)
	tr.RecordUse(t.Context(), "exa", now)
	tr.RecordUse(t.Context(), "exa", now)

	restarted := newTracker(t, store)
	if u := restarted.Usage("exa", now); u.MonthUsed != 2 {
		t.Errorf("usage after a restart = %+v, want 2 this month", u)
	}

	store.err = errors.New("database down")
	restarted.RecordUse(t.Context(), "exa", now)
	if u := restarted.Usage("exa", now); u.MonthUsed != 3 {
		t.Errorf("a failed save lost the count: %+v", u)
	}
	if _, err := search.NewTracker(t.Context(), store, discard()); err == nil {
		t.Error("NewTracker over a failing store succeeded")
	}
}

func TestRetryDeadline(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	with := func(k, v string) error {
		return &search.HTTPError{Status: 429, Message: "HTTP 429", Header: http.Header{k: {v}}}
	}
	cases := []struct {
		name string
		err  error
		want time.Time
	}{
		{"seconds", with("Retry-After", "3600"), now.Add(time.Hour)},
		{"date", with("Retry-After", now.Add(time.Hour).Format(http.TimeFormat)), now.Add(time.Hour)},
		{"reset seconds", with("X-Ratelimit-Reset", "1786798800"), time.Unix(1786798800, 0)},
		{"reset milliseconds", with("X-Ratelimit-Reset", "1786798800000"), time.Unix(1786798800, 0)},
		{"seconds past the cap", with("Retry-After", "1e30"), now.Add(24 * time.Hour)},
		{"negative seconds", with("Retry-After", "-5"), time.Time{}},
		{"NaN seconds", with("Retry-After", "NaN"), time.Time{}},
		{"reset past the cap", with("X-Ratelimit-Reset", "99999999999"), now.Add(24 * time.Hour)},
		{"no header", &search.HTTPError{Status: 429}, time.Time{}},
		{"not an HTTP error", errors.New("boom"), time.Time{}},
	}
	for _, c := range cases {
		if got := search.RetryDeadline(c.err, now); !got.Equal(c.want) {
			t.Errorf("%s: RetryDeadline = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestDescribe(t *testing.T) {
	if got := search.Describe(&search.HTTPError{Status: 404, Message: "HTTP 404"}); got != "HTTP 404" {
		t.Errorf("Describe = %q, want the status once", got)
	}
	if got := search.Describe(&search.HTTPError{Status: 403, Message: "forbidden"}); got != "forbidden - 403" {
		t.Errorf("Describe = %q", got)
	}
	if got := search.Describe(context.DeadlineExceeded); got != "timed out" {
		t.Errorf("Describe(deadline) = %q", got)
	}
	if got := search.Describe(errors.New("boom")); !strings.Contains(got, "boom") {
		t.Errorf("Describe = %q", got)
	}
}
