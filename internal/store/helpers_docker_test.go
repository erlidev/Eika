//go:build docker

package store_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

// ptr returns a pointer to v, for the optional profile settings.
func ptr[T any](v T) *T { return &v }

// stated returns a stored count, or -1 for one that is NULL, which no stored
// count is.
func stated(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// sameJSON reports whether two documents are equal as JSON, which is all a
// jsonb column promises about what it gives back.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// newSession creates a session in a computer of its own.
func newSession(t *testing.T, st *store.Store, sess store.Session) store.Session {
	t.Helper()
	cx, err := st.CreateComputer(t.Context(), store.Computer{
		Name: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess.ComputerID = cx.ID
	if sess.Kind == "" {
		sess.Kind, sess.Agent, sess.Name = store.SessionRoutine, "eika", "test"
	}
	out, err := st.CreateSession(t.Context(), sess)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return out
}
