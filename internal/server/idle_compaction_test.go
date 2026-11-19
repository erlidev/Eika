package server

import (
	"context"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/inference"
)

func TestIdleCompactionReservation(t *testing.T) {
	r := newRuns(nil)
	r.lastImportant = time.Now().Add(-31 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle, err := r.reserve("home", "", job{idle: true, priority: inference.Maintenance, idleFor: 30 * time.Minute}, cancel)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.reserve("other", "", job{}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("important work did not cancel idle compaction")
	}
	if !idle.idle {
		t.Fatal("idle reservation lost its kind")
	}
	if _, err := r.reserve("another", "", job{idle: true, priority: inference.Maintenance, idleFor: 30 * time.Minute}, func() {}); err == nil {
		t.Fatal("idle compaction started while important work was active")
	}
}

func TestIdleCompactionTimeBoundary(t *testing.T) {
	r := newRuns(nil)
	work := job{idle: true, priority: inference.Maintenance, idleFor: 30 * time.Minute}
	r.lastImportant = time.Now().Add(-29 * time.Minute)
	if _, err := r.reserve("home", "", work, func() {}); err == nil {
		t.Fatal("reserved before idle interval")
	}
	r.lastImportant = time.Now().Add(-30 * time.Minute)
	if _, err := r.reserve("home", "", work, func() {}); err != nil {
		t.Fatal(err)
	}
}

func TestIdleCompactionSettingValidation(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{settingCompactIdleFill, "60", true}, {settingCompactIdleFill, "0", false},
		{settingCompactIdleFill, "101", false}, {settingCompactIdleMinutes, "0", true},
		{settingCompactIdleMinutes, "30", true}, {settingCompactIdleMinutes, "-1", false},
	} {
		err := s.validateSetting(context.Background(), tc.key, []byte(tc.value))
		if (err == nil) != tc.valid {
			t.Errorf("%s=%s: %v", tc.key, tc.value, err)
		}
	}
}
