package skill_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/skill"
)

// words counts a token per space-separated word, exactly or not.
func words(exact bool) skill.CountFunc {
	return func(_ context.Context, text string) (int, bool, error) {
		return len(strings.Fields(text)), exact, nil
	}
}

func TestCatalogListsLoadableSkillsSortedAndNothingWhenNone(t *testing.T) {
	rec := func(name string, enabled bool, broken string) skill.Record {
		return skill.Record{Skill: skill.Skill{Name: name, Description: "Does\n  " + name}, Enabled: enabled, Broken: broken}
	}
	if got := skill.Catalog([]skill.Record{rec("a", false, "")}); got != "" {
		t.Fatalf("catalog with none enabled = %q, want empty", got)
	}
	got := skill.Catalog([]skill.Record{rec("zed", true, ""), rec("off", false, ""), rec("bad", true, skill.BrokenMissing), rec("alpha", true, "")})
	want := "## Skills (skill_use <name> to load one)\n- alpha: Does alpha\n- zed: Does zed\n"
	if got != want {
		t.Fatalf("catalog = %q, want %q", got, want)
	}
}

func TestSetEnabledChecksTheBudgetUnderTheLockAndRefusesBrokenSkills(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, n := range []string{"aa", "bb"} {
		if _, err := e.in.Install(ctx, skillZip(t, n, "body")); err != nil {
			t.Fatal(err)
		}
	}
	check := func(count skill.CountFunc) func([]skill.Record) error {
		return func(after []skill.Record) error { return skill.CheckCatalogBudget(ctx, count, after) }
	}
	if _, err := e.in.SetEnabled(ctx, "aa", true, check(words(false))); !errors.Is(err, skill.ErrNotExact) || e.idx.rows["aa"].Enabled {
		t.Fatalf("estimate: err = %v, enabled = %v, want ErrNotExact and unchanged", err, e.idx.rows["aa"].Enabled)
	}
	if rec, err := e.in.SetEnabled(ctx, "aa", true, check(words(true))); err != nil || !rec.Enabled || !e.idx.rows["aa"].Enabled {
		t.Fatalf("enable: %+v, %v", rec, err)
	}
	// A counter that finds the catalog full refuses the second skill and
	// leaves it disabled; disabling is never refused.
	full := func(context.Context, string) (int, bool, error) { return skill.CatalogBudgetTokens + 1, true, nil }
	if _, err := e.in.SetEnabled(ctx, "bb", true, check(full)); !errors.Is(err, skill.ErrCatalogOverBudget) || e.idx.rows["bb"].Enabled {
		t.Fatalf("over budget: err = %v, enabled = %v", err, e.idx.rows["bb"].Enabled)
	}
	if rec, err := e.in.SetEnabled(ctx, "aa", false, check(full)); err != nil || rec.Enabled {
		t.Fatalf("disable: %+v, %v", rec, err)
	}
	r := e.idx.rows["bb"]
	r.Broken = skill.BrokenMissing
	e.idx.rows["bb"] = r
	if _, err := e.in.SetEnabled(ctx, "bb", true, nil); !errors.Is(err, skill.ErrBroken) {
		t.Fatalf("broken enable: %v, want ErrBroken", err)
	}
	if _, err := e.in.SetEnabled(ctx, "nope", true, nil); !errors.Is(err, skill.ErrNotFound) {
		t.Fatalf("unknown enable: %v", err)
	}
	// A bad zip is the archive's fault, a missing volume is not.
	if _, err := e.in.Install(ctx, []byte("not a zip")); err == nil || !errors.As(err, new(*skill.InvalidError)) {
		t.Fatalf("bad zip: %v, want an InvalidError", err)
	}
}
