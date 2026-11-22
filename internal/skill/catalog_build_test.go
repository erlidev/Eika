package skill

import (
	"context"
	"strings"
	"testing"
)

func words(_ context.Context, s string) (int, bool, error) { return len(strings.Fields(s)), true, nil }

func rec(name, desc string, enabled bool, broken string) Record {
	return Record{Skill: Skill{Name: name, Description: desc}, Enabled: enabled, Broken: broken}
}

func TestBuildCatalog(t *testing.T) {
	ctx := context.Background()
	in := []Record{
		rec("zeta", "Last\none.", true, ""),
		rec("off", "disabled", false, ""),
		rec("bad", "broken", true, BrokenChanged),
		rec("alpha", "First.", true, ""),
	}
	c, err := BuildCatalog(ctx, words, in)
	if err != nil {
		t.Fatal(err)
	}
	want := CatalogHeading + "\n- alpha: First.\n- zeta: Last one.\n"
	if c.Text != want || c.Listed != 2 || c.Overflow != 0 {
		t.Fatalf("got %+v", c)
	}
	if c, err := BuildCatalog(ctx, words, in[1:3]); err != nil || c.Text != "" {
		t.Fatalf("no loadable skill: %+v, %v", c, err)
	}
	// Whole entries only, within the budget, with the overflow counted in it.
	var many []Record
	for i := range 400 {
		many = append(many, rec(string(rune('a'+i/26))+string(rune('a'+i%26)), strings.Repeat("w ", 9), true, ""))
	}
	c, err = BuildCatalog(ctx, words, many)
	if err != nil || c.Tokens > CatalogBudgetTokens || c.Overflow == 0 || c.Listed+c.Overflow != 400 {
		t.Fatalf("got %d listed, %d overflow, %d tokens, %v", c.Listed, c.Overflow, c.Tokens, err)
	}
	// An estimate never builds a catalog.
	est := func(context.Context, string) (int, bool, error) { return 1, false, nil }
	if _, err := BuildCatalog(ctx, est, in); err == nil {
		t.Fatal("built from an estimate")
	}
}
