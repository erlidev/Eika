//go:build docker

package session_test

import (
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestHomeMessagesRecordChannel(t *testing.T) {
	st := storetest.Open(t)
	home, err := st.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messages := session.NewStore(session.NewTree(st))
	messages.SetChannel("web")
	for _, m := range []provider.Message{provider.UserMessage("hi"), provider.AssistantMessage("hello", nil), provider.ToolResultMessage("call", "result", false)} {
		if err := messages.Append(t.Context(), home.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := st.SessionPath(t.Context(), home.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if entries[0].Channel != "web" || entries[1].Channel != "web" || entries[2].Channel != "" {
		t.Fatalf("channels = %q, %q, %q", entries[0].Channel, entries[1].Channel, entries[2].Channel)
	}
}
