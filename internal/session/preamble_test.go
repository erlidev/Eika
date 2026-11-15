package session

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestPreambleReplaysLatestSnapshot(t *testing.T) {
	first, err := TextEntry(store.KindPreamble, "first\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := TextEntry(store.KindPreamble, "second\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		entries []store.Entry
		want    string
	}{{nil, ""}, {[]store.Entry{first}, "first\n"}, {[]store.Entry{first, second}, "second\n"}} {
		got, err := Preamble(tc.entries)
		if err != nil || got != tc.want {
			t.Fatalf("Preamble = %q, %v; want %q", got, err, tc.want)
		}
	}
}
