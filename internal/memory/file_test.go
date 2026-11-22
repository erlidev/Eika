package memory_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/memory"
)

const file = `---
name: prefers-metric-units
description: "Use for measurements: recipes, weather and distances."
type: preference
source: 'session:123 entry:456'
created: 2026-10-29T14:03:00Z
updated: 2026-10-29T14:03:00Z
expires:
pinned: false
state: active
---
The user prefers metric units.
`

func TestParseAndRender(t *testing.T) {
	for _, input := range []string{file, strings.ReplaceAll(file, "\n", "\r\n")} {
		f, err := memory.Parse("prefers-metric-units.md", []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if f.Source != "session:123 entry:456" || f.State != "active" || !strings.HasPrefix(f.Body, "The user") {
			t.Fatalf("unexpected memory: %+v", f)
		}
		rendered, err := f.Render("prefers-metric-units.md")
		if err != nil {
			t.Fatal(err)
		}
		again, err := memory.Parse("prefers-metric-units.md", rendered)
		if err != nil {
			t.Fatal(err)
		}
		renderedAgain, err := again.Render("prefers-metric-units.md")
		if err != nil || !bytes.Equal(rendered, renderedAgain) || again != f {
			t.Fatalf("round trip changed memory: %v", err)
		}
	}
}

func TestRejectInvalidFiles(t *testing.T) {
	cases := map[string]struct{ name, text string }{
		"path":            {"../prefers-metric-units.md", file},
		"slug":            {"Bad.md", strings.Replace(file, "prefers-metric-units", "Bad", 1)},
		"unknown field":   {"prefers-metric-units.md", strings.Replace(file, "state: active", "state: active\ntrust: user", 1)},
		"duplicate field": {"prefers-metric-units.md", strings.Replace(file, "state: active", "state: active\nstate: pending", 1)},
		"state":           {"prefers-metric-units.md", strings.Replace(file, "state: active", "state: trusted", 1)},
		"date":            {"prefers-metric-units.md", strings.Replace(file, "expires:", "expires: 2026-02-30", 1)},
		"large body":      {"prefers-metric-units.md", file + strings.Repeat("x", memory.MaxBodyBytes)},
		"large file":      {"prefers-metric-units.md", strings.Repeat("x", memory.MaxFileBytes+1)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := memory.Parse(tc.name, []byte(tc.text)); err == nil {
				t.Fatal("accepted invalid file")
			}
		})
	}
	f, err := memory.Parse("prefers-metric-units.md", []byte(file))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"active", "pending", "archived"} {
		f.State = state
		f.Body = strings.Repeat("x", memory.MaxBodyBytes)
		if _, err := f.Render("prefers-metric-units.md"); err != nil {
			t.Fatal(err)
		}
	}
}
