//go:build docker

package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/erlidev/eika/internal/subagent"
)

func TestSubagentLimitsFollowTheSettings(t *testing.T) {
	st := storetest.Open(t)
	limits := subagentLimits(st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if got := limits(t.Context()); got != (subagent.Limits{MaxDepth: defaultSubagentDepth, MaxChildren: defaultSubagentChildren}) {
		t.Errorf("limits with no settings = %+v, want the defaults", got)
	}
	for key, value := range map[string]string{settingSubagentDepth: "3", settingSubagentChildren: "7"} {
		if err := st.SetSetting(t.Context(), key, json.RawMessage(value)); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	if got := limits(t.Context()); got != (subagent.Limits{MaxDepth: 3, MaxChildren: 7}) {
		t.Errorf("limits = %+v, want the settings", got)
	}
	// A value of the wrong shape, written before validation existed or by
	// hand, falls back to the default rather than stopping every spawn.
	if err := st.SetSetting(t.Context(), settingSubagentDepth, json.RawMessage(`"deep"`)); err != nil {
		t.Fatalf("set depth: %v", err)
	}
	if got := limits(t.Context()); got.MaxDepth != defaultSubagentDepth || got.MaxChildren != 7 {
		t.Errorf("limits = %+v, want the default depth", got)
	}
}
