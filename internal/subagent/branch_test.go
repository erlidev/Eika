package subagent

import (
	"errors"
	"testing"
)

func TestCheckName(t *testing.T) {
	cases := []struct {
		name  string
		given string
		ok    bool
	}{
		{"a word", "review", true},
		{"digits and dashes", "fix-123_a", true},
		{"one character", "a", true},
		{"empty", "", false},
		{"a space", "fix the bug", false},
		{"a slash", "feature/fix", false},
		{"a dot", "fix..er", false},
		{"leading dash, which git reads as an option", "-fix", false},
		{"a shell command", "fix; touch /tmp/pwned", false},
		{"a path that walks out", "../../etc", false},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkName(c.given)
			if c.ok && err != nil {
				t.Fatalf("checkName(%q) = %v, want no error", c.given, err)
			}
			if !c.ok && !errors.Is(err, ErrBadName) {
				t.Fatalf("checkName(%q) = %v, want ErrBadName", c.given, err)
			}
		})
	}
}

func TestChildBranch(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		suffix string
		want   string
	}{
		// A child's branch is never a path below its parent's: a repository
		// holds either refs/heads/main or refs/heads/main/fix, never both.
		{"below a branch", "main", "fix", "main-fix"},
		{"below a branch that is a path", "feature/login", "fix", "feature/login-fix"},
		{"below a workspace with no branch", "", "fix", "fix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := childBranch(c.parent, c.suffix); got != c.want {
				t.Errorf("childBranch(%q, %q) = %q, want %q", c.parent, c.suffix, got, c.want)
			}
		})
	}
}
