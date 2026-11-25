//go:build docker

package store_test

import (
	"testing"
)

// A provider that exists before the protocol was a column keeps speaking Chat
// Completions, with nothing marked as the user's own, until a Responses check
// finds otherwise.
func TestUpgradeFrom0006LeavesProvidersOnChat(t *testing.T) {
	st, _ := upgradeFrom(t, "0006_model_calls",
		`INSERT INTO providers (id, name, kind, base_url, backend, overridden) VALUES ('p', 'bench', 'openai', 'http://m.invalid/v1', 'llamacpp', '{kv}')`,
	)
	p, err := st.Provider(t.Context(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.API != "chat" || p.Backend != "llamacpp" || len(p.Overridden) != 1 || p.Overridden[0] != "kv" {
		t.Errorf("provider after the upgrade = %+v, want the chat api and everything else as it was", p)
	}
}
