//go:build docker

package session_test

import (
	"fmt"
	"testing"

	"github.com/erlidev/eika/internal/session"
)

// The same scenario as TestEachRequestBeginsWithTheBytesOfTheOneBeforeItJSONEntries,
// with the conversation stored in PostgreSQL and read back at the start of
// every run, as the server does. The column is where a payload can change
// without the Go code writing anything different, which an in-memory store
// cannot show.
func TestEachRequestBeginsWithTheBytesOfTheOneBeforeItPostgres(t *testing.T) {
	for _, api := range []string{"chat", "responses"} {
		for _, preserve := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/preserve=%v", api, preserve), func(t *testing.T) {
				tree, sess := newTree(t)
				checkPrefixStability(t, api, preserve, session.NewStore(tree), sess.ID)
			})
		}
	}
}
