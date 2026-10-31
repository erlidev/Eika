package server

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestTheEffectiveWindowIsTheSmallerOfTheConfiguredAndTheProbed(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		probed     int
		want       int
	}{
		{"nothing probed", 200_000, 0, 200_000},
		{"the endpoint holds less", 200_000, 131_072, 131_072},
		{"the endpoint holds more", 32_768, 131_072, 32_768},
		{"the same", 131_072, 131_072, 131_072},
		{"a negative probe is no probe", 4096, -1, 4096},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveWindow(tc.configured, tc.probed); got != tc.want {
				t.Errorf("effectiveWindow(%d, %d) = %d, want %d", tc.configured, tc.probed, got, tc.want)
			}
		})
	}
}

func TestTheProbedWindowIsReadFromTheLastProbe(t *testing.T) {
	tests := []struct {
		name         string
		capabilities string
		want         int
	}{
		{"a stated window", `{"window": 131072, "counting": "chat"}`, 131_072},
		{"no window", `{"counting": "none"}`, 0},
		{"a provider never probed", `{}`, 0},
		{"unreadable", `not json`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := store.Provider{Capabilities: json.RawMessage(tc.capabilities)}
			if got := probedWindow(p); got != tc.want {
				t.Errorf("probedWindow = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestARunConfigurationIsJudgedAgainstTheEffectiveWindow(t *testing.T) {
	c := runConfig{model: store.Model{ContextWindow: 200_000}, modelOK: true}
	if got := c.window(); got != 200_000 {
		t.Errorf("window with no probe = %d, want the configured 200000", got)
	}
	c = c.withProbe(store.Provider{Capabilities: json.RawMessage(`{"window": 131072}`)})
	if got := c.window(); got != 131_072 {
		t.Errorf("window after a probe = %d, want the probed 131072", got)
	}
}
