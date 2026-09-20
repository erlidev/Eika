package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Bounds on how long a failing backend is left alone.
const (
	// cooldownBase is the cooldown after one failure. Each consecutive
	// failure doubles it.
	cooldownBase = 15 * time.Minute
	// cooldownMax caps the cooldown, so a backend that was down overnight is
	// tried again the next morning.
	cooldownMax = 6 * time.Hour
	// maxRetryWait caps the deadline a backend names, so a bogus header
	// cannot shelve a backend for years.
	maxRetryWait = 24 * time.Hour
)

// Limit is a quota on one bucket. Zero means unlimited.
type Limit struct {
	Day   int `json:"day,omitempty"`
	Month int `json:"month,omitempty"`
}

// Usage is one bucket's counters and cooldown. Day and Month are the UTC day
// ("2026-09-18") and month ("2026-09") the counters count in; a counter from
// an earlier day or month reads as zero.
type Usage struct {
	Day           string    `json:"day"`
	DayUsed       int       `json:"day_used"`
	Month         string    `json:"month"`
	MonthUsed     int       `json:"month_used"`
	CooldownUntil time.Time `json:"cooldown_until,omitzero"`
	FailStreak    int       `json:"fail_streak,omitempty"`
}

// UsageStore persists the counters, so that a monthly quota survives a
// restart. The store package implements it.
type UsageStore interface {
	LoadSearchUsage(ctx context.Context) (map[string]Usage, error)
	SaveSearchUsage(ctx context.Context, name string, u Usage) error
}

// Tracker counts each bucket's requests against its quota and cools down a
// bucket whose backend failed. A bucket is usually one backend; the GitHub
// sources and fetch's GitHub reads share one. The Tracker holds the counters
// in memory and writes every change through to the store. Persistence is
// advisory: a failed write is logged and the search goes on.
type Tracker struct {
	store UsageStore
	log   *slog.Logger

	mu    sync.Mutex
	usage map[string]*Usage
	// saveMu keeps write-throughs in the order the changes were made, so an
	// older snapshot never overwrites a newer one in the store. It is taken
	// before mu is released.
	saveMu sync.Mutex
}

// NewTracker returns a Tracker holding the counters the store has. A nil
// store keeps them in memory only.
func NewTracker(ctx context.Context, store UsageStore, log *slog.Logger) (*Tracker, error) {
	t := &Tracker{store: store, log: log, usage: make(map[string]*Usage)}
	if store == nil {
		return t, nil
	}
	saved, err := store.LoadSearchUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("load search usage: %w", err)
	}
	for name, u := range saved {
		t.usage[name] = &u
	}
	return t, nil
}

// entry returns a bucket's counters, rolled over to now's day and month. The
// caller holds t.mu.
func (t *Tracker) entry(name string, now time.Time) *Usage {
	day, month := now.UTC().Format(time.DateOnly), now.UTC().Format("2006-01")
	u, ok := t.usage[name]
	if !ok {
		u = &Usage{Day: day, Month: month}
		t.usage[name] = u
	}
	if u.Day != day {
		u.Day, u.DayUsed = day, 0
	}
	if u.Month != month {
		u.Month, u.MonthUsed = month, 0
	}
	return u
}

// Usage returns a bucket's counters as of now.
func (t *Tracker) Usage(name string, now time.Time) Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.entry(name, now)
}

// Blocked says why a bucket may not be used now: "cooling down 12m", "daily
// quota spent", or "monthly quota spent". It is empty when the bucket is
// usable.
func (t *Tracker) Blocked(name string, limit Limit, now time.Time) string {
	u := t.Usage(name, now)
	switch {
	case u.CooldownUntil.After(now):
		return fmt.Sprintf("cooling down %dm", int(math.Ceil(u.CooldownUntil.Sub(now).Minutes())))
	case limit.Day > 0 && u.DayUsed >= limit.Day:
		return "daily quota spent"
	case limit.Month > 0 && u.MonthUsed >= limit.Month:
		return "monthly quota spent"
	}
	return ""
}

// RecordUse counts one request against a bucket and clears its cooldown,
// since the backend answered.
func (t *Tracker) RecordUse(ctx context.Context, name string, now time.Time) {
	t.update(ctx, name, now, func(u *Usage) {
		u.DayUsed++
		u.MonthUsed++
		u.FailStreak = 0
		u.CooldownUntil = time.Time{}
	})
}

// RecordFailure cools a bucket down, doubling the cooldown for each
// consecutive failure up to a ceiling. A deadline the server named, such as
// GitHub's rate-limit reset, wins when it is later.
func (t *Tracker) RecordFailure(ctx context.Context, name string, now, until time.Time) {
	t.update(ctx, name, now, func(u *Usage) {
		u.FailStreak++
		backoff := min(cooldownBase<<min(u.FailStreak-1, 10), cooldownMax)
		u.CooldownUntil = now.Add(backoff)
		if until.After(u.CooldownUntil) {
			u.CooldownUntil = until
		}
	})
}

// update applies change to a bucket and writes the result through to the
// store.
func (t *Tracker) update(ctx context.Context, name string, now time.Time, change func(*Usage)) {
	t.mu.Lock()
	u := t.entry(name, now)
	change(u)
	saved := *u
	if t.store == nil {
		t.mu.Unlock()
		return
	}
	t.saveMu.Lock()
	t.mu.Unlock()
	defer t.saveMu.Unlock()
	// A search cancelled just after its backend answered still counts.
	if err := t.store.SaveSearchUsage(context.WithoutCancel(ctx), name, saved); err != nil {
		t.log.Warn("save search usage", "bucket", name, "err", err)
	}
}

// RetryDeadline reads when a rate-limited backend says to come back: the
// Retry-After header in seconds or as an HTTP date, else X-RateLimit-Reset in
// unix seconds (or milliseconds, which some APIs send). It is zero when the
// error names no deadline, and at most maxRetryWait away.
func RetryDeadline(err error, now time.Time) time.Time {
	at := retryDeadline(err, now)
	if limit := now.Add(maxRetryWait); at.After(limit) {
		return limit
	}
	return at
}

// retryDeadline is RetryDeadline without the cap.
func retryDeadline(err error, now time.Time) time.Time {
	var h *HTTPError
	if !errors.As(err, &h) || h.Header == nil {
		return time.Time{}
	}
	if after := h.Header.Get("Retry-After"); after != "" {
		if seconds, err := strconv.ParseFloat(after, 64); err == nil {
			// NaN fails both comparisons; a huge value would overflow.
			if !(seconds >= 0) {
				return time.Time{}
			}
			return now.Add(time.Duration(min(seconds, maxRetryWait.Seconds()) * float64(time.Second)))
		}
		if at, err := http.ParseTime(after); err == nil {
			return at
		}
	}
	reset, err := strconv.ParseFloat(h.Header.Get("X-RateLimit-Reset"), 64)
	if err != nil || reset <= 0 {
		return time.Time{}
	}
	if reset > 1e11 {
		reset /= 1000
	}
	if reset > float64(now.Add(maxRetryWait).Unix()) {
		return now.Add(maxRetryWait)
	}
	return time.Unix(int64(reset), 0)
}
