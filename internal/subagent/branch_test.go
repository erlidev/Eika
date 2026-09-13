package subagent

import (
	"errors"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
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
	const id = "abcdef1234567890abcd"
	cases := []struct {
		name   string
		parent string
		suffix string
		want   string
	}{
		// A child's branch is never a path below its parent's: a repository
		// holds either refs/heads/main or refs/heads/main/fix, never both.
		// The tag from the child's id is what keeps two children a parent
		// named the same from writing over each other in the hub.
		{"below a branch", "main", "fix", "main-fix-abcdef"},
		{"below a branch that is a path", "feature/login", "fix", "feature/login-fix-abcdef"},
		{"below a workspace with no branch", "", "fix", "fix-abcdef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := childBranch(c.parent, c.suffix, id); got != c.want {
				t.Errorf("childBranch(%q, %q, %q) = %q, want %q", c.parent, c.suffix, id, got, c.want)
			}
		})
	}
}

func TestChildBranchesOfTheSameNameDiffer(t *testing.T) {
	first := childBranch("main", "fix", store.NewID())
	second := childBranch("main", "fix", store.NewID())
	if first == second {
		t.Errorf("two children named fix both got branch %q, want branches of their own", first)
	}
}

func TestDescribeSpeaksForAChildThatSaidNothing(t *testing.T) {
	cases := map[string]struct {
		result builtin.AgentResult
		want   []string
	}{
		"with changes": {
			builtin.AgentResult{State: "aborted", DiffStat: " notes.txt | 1 +"},
			[]string{"aborted", "notes.txt"},
		},
		"with none": {
			builtin.AgentResult{State: "error"},
			[]string{"error", "changed nothing"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := describe(c.result)
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("describe = %q, want %q in it", got, want)
				}
			}
		})
	}
}
