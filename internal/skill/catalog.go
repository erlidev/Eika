package skill

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CatalogBudgetTokens is the exact token budget of the skills catalog: enabling
// a skill that would take the catalog past it is refused (question 5).
const CatalogBudgetTokens = 2000

var (
	// ErrCatalogOverBudget refuses an enable that would put the catalog over
	// CatalogBudgetTokens.
	ErrCatalogOverBudget = errors.New("skills catalog exceeds the token budget")
	// ErrNotExact is returned when the counter did not count exactly.
	ErrNotExact = errors.New("token count is not exact")
)

// CountFunc counts the tokens of text with the model's own counter; exact is
// false for an estimate, which is never used to judge the budget.
type CountFunc func(ctx context.Context, text string) (tokens int, exact bool, err error)

// Catalog renders the catalog block: the loadable skills of recs sorted by
// name, one line each, under the heading of the design. It is empty when none
// is loadable, so a preamble then has no section.
func Catalog(recs []Record) string {
	var loadable []Record
	for _, r := range recs {
		if r.Loadable() {
			loadable = append(loadable, r)
		}
	}
	if len(loadable) == 0 {
		return ""
	}
	slices.SortFunc(loadable, func(a, b Record) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	b.WriteString("## Skills (skill_use <name> to load one)\n")
	for _, r := range loadable {
		fmt.Fprintf(&b, "- %s: %s\n", r.Name, strings.Join(strings.Fields(r.Description), " "))
	}
	return b.String()
}

// CheckCatalogBudget counts the catalog recs would render and returns
// ErrCatalogOverBudget (with the count) when it is over the budget, or
// ErrNotExact when the count is an estimate.
func CheckCatalogBudget(ctx context.Context, count CountFunc, recs []Record) error {
	text := Catalog(recs)
	if text == "" {
		return nil
	}
	n, exact, err := count(ctx, text)
	if err != nil {
		return fmt.Errorf("count skills catalog: %w", err)
	}
	if !exact {
		return ErrNotExact
	}
	if n > CatalogBudgetTokens {
		return fmt.Errorf("%w: %d of %d tokens", ErrCatalogOverBudget, n, CatalogBudgetTokens)
	}
	return nil
}
