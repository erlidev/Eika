//go:build docker

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

// A payload is read back as the text it was written as. A prompt cache reads
// the bytes of the conversation a request sends, and the conversation is read
// from these payloads at the start of every run; jsonb would write a document
// back with its keys reordered and a space after every colon.
func TestAnEntryPayloadComesBackAsTheTextItWasWrittenAs(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	cx, err := st.CreateComputer(ctx, store.Computer{Name: "payloads", State: "running"})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "payloads"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	payloads := []string{
		`{"role":"assistant","tool_calls":[{"id":"c","name":"echo","arguments":{"z":1,"a":2}}]}`,
		"{\n  \"role\": \"user\",\n  \"content\": \"a  b\"\n}",
		`{"role":"user","content":"café <&>","n":1.50,"e":1e5}`,
	}
	for _, payload := range payloads {
		if _, err := st.AppendEntry(ctx, sess.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
	}
	path, err := st.SessionPath(ctx, sess.ID)
	if err != nil {
		t.Fatalf("SessionPath: %v", err)
	}
	if len(path) != len(payloads) {
		t.Fatalf("path has %d entries, want %d", len(path), len(payloads))
	}
	for i, e := range path {
		if string(e.Payload) != payloads[i] {
			t.Errorf("entry %d came back as %s, want %s", i+1, e.Payload, payloads[i])
		}
	}
}
