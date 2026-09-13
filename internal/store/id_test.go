package store_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestNewID(t *testing.T) {
	t.Parallel()
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id := store.NewID()
		if len(id) != 20 {
			t.Fatalf("NewID() = %q, want 20 characters", id)
		}
		if i := strings.IndexFunc(id, func(r rune) bool { return !strings.ContainsRune(alphabet, r) }); i >= 0 {
			t.Fatalf("NewID() = %q, character %d is not lowercase base32", id, i)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}
		seen[id] = true
	}
}
