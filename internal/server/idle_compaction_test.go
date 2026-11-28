package server

import (
	"context"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/store"
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

func TestScreenSettingValidation(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{settingScreenRequired, "true", true}, {settingScreenRequired, `"true"`, false},
		{settingScreenThreshold, "0.9", true}, {settingScreenThreshold, "0", false},
		{settingScreenThreshold, "1.1", false}, {settingScreenThreshold, "null", true},
		{settingMemoryScreenThreshold, "0.99", true}, {settingMemoryScreenThreshold, "0", false}, {settingMemoryScreenThreshold, "1.1", false},
		{settingWatchThreshold, "0.5", true}, {settingWatchThreshold, "0", false},
		{settingSensitiveList, `["\\.env"]`, true}, {settingSensitiveList, `["("]`, false}, {settingSensitiveList, `"x"`, false},
		{settingSinkLimits, `{"fan_out_urls":8}`, true}, {settingSinkLimits, `{"upload_ratio":0}`, false},
		{settingTripwires, `{"followed":true}`, true}, {settingTripwires, `{"blocked_limit":0}`, false},
	} {
		err := s.validateSetting(context.Background(), tc.key, []byte(tc.value))
		if (err == nil) != tc.valid {
			t.Errorf("%s=%s: %v", tc.key, tc.value, err)
		}
	}
}

func TestScreenSettingsUseStoredValuesAndDefaults(t *testing.T) {
	rows := []store.Setting{{Key: settingScreenRequired, Value: []byte(`true`)}, {Key: settingScreenThreshold, Value: []byte(`0.7`)}}
	threshold, required, err := parseScreenSettings(rows, 0.9)
	if err != nil || threshold != 0.7 || !required {
		t.Fatalf("settings %v %v: %v", threshold, required, err)
	}
	rows[0].Value, rows[1].Value = []byte(`null`), []byte(`null`)
	threshold, required, err = parseScreenSettings(rows, 0.9)
	if err != nil || threshold != 0.9 || required {
		t.Fatalf("defaults %v %v: %v", threshold, required, err)
	}
}
