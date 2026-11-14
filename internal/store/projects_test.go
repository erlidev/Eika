package store_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestProjectValidation(t *testing.T) {
	for _, slug := range []string{"a", "taxes-2026", strings.Repeat("x", 80)} {
		if err := store.ValidateProjectSlug(slug); err != nil {
			t.Fatal(err)
		}
	}
	for _, slug := range []string{"", "A", "../a", "a/b", "-a", "a-", "a--b", "a_b", "a\n", strings.Repeat("x", 81)} {
		t.Run("slug "+slug, func(t *testing.T) {
			if !errors.Is(store.ValidateProjectSlug(slug), store.ErrInvalidProject) {
				t.Fatal("accepted invalid slug")
			}
		})
	}
	valid := store.ProjectFields{Name: "Taxes", Status: "active", Directory: "/home/eika/projects/taxes"}
	for _, status := range []string{"active", "paused", "done", "archived"} {
		p := valid
		p.Status = status
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{"/home/eika/old work", "/tmp/repo"} {
		p := valid
		p.Directory = directory
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{"", "/", "relative", "~/repo", "/a/../b", "/a/..", "/a/./b", "/a//b", "/a/", "/a\\b", "/a\x00b", "/a\nb", "/a\tb", "/a\x7fb", strings.Repeat("/x", 2049)} {
		t.Run(fmt.Sprintf("directory %q", directory[:min(len(directory), 60)]), func(t *testing.T) {
			p := valid
			p.Directory = directory
			if !errors.Is(p.Validate(), store.ErrInvalidProject) {
				t.Fatal("accepted invalid directory")
			}
		})
	}
	for _, p := range []store.ProjectFields{
		{Name: " ", Status: "active", Directory: "/a"},
		{Name: strings.Repeat("x", 201), Status: "active", Directory: "/a"},
		{Name: "a", Description: strings.Repeat("x", 4097), Status: "active", Directory: "/a"},
		{Name: "a", Description: "a\x00b", Status: "active", Directory: "/a"},
		{Name: "a", Description: string([]byte{255}), Status: "active", Directory: "/a"},
		{Name: "a", Status: "unknown", Directory: "/a"},
	} {
		if !errors.Is(p.Validate(), store.ErrInvalidProject) {
			t.Fatalf("accepted %+v", p)
		}
	}
}
