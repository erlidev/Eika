package session

import (
	"reflect"
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

func TestSnapshotPinsTheDefinitionItWasTakenWith(t *testing.T) {
	prompt := "You are Eika."
	pinned, err := PreambleEntry("pinned\n", &prompt, []string{"project_list", "subagent_start"})
	if err != nil {
		t.Fatal(err)
	}
	none, err := PreambleEntry("no tools\n", new(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := TextEntry(store.KindPreamble, "old\n")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		entries []store.Entry
		text    string
		prompt  *string
		tools   []string
	}{
		{"a pinned definition", []store.Entry{old, pinned}, "pinned\n", &prompt, []string{"project_list", "subagent_start"}},
		{"an empty prompt and no tools", []store.Entry{none}, "no tools\n", new(""), []string{}},
		// A snapshot stored before definitions were pinned pins none, even
		// after one that did.
		{"a text-only snapshot", []store.Entry{pinned, old}, "old\n", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SnapshotOf(tc.entries)
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != tc.text || !reflect.DeepEqual(got.Prompt, tc.prompt) || !reflect.DeepEqual(got.Tools, tc.tools) {
				t.Errorf("SnapshotOf = %q, %v, %v; want %q, %v, %v", got.Text, got.Prompt, got.Tools, tc.text, tc.prompt, tc.tools)
			}
			// The entries API reads a preamble's text alone.
			text, err := TextOf(tc.entries[len(tc.entries)-1])
			if err != nil || text != tc.text {
				t.Errorf("TextOf = %q, %v; want %q", text, err, tc.text)
			}
		})
	}
}
