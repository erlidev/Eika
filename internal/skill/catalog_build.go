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

// CatalogHeading is the first line of the catalog section; it is the heading of
// docs/design/skills-and-plugins.md.
const CatalogHeading = "## Skills (skill_use <name> to load one)"

// CatalogSection is a catalog built for a snapshot.
type CatalogSection struct {
	Text     string
	Tokens   int
	Listed   int
	Overflow int // loadable skills that did not fit
}

// BuildCatalog is the catalog of recs within CatalogBudgetTokens, counted
// exactly: the largest prefix of the sorted loadable skills that fits, whole
// entries only, with an overflow line (inside the budget) for the rest. Enabling
// a skill is refused when this reports Overflow, so the check counts exactly the
// text the preamble will hold; overflow after that is the edge a changed counter
// can still cause. No loadable skill means no section. A count that is not
// exact fails the build, and the caller keeps its previous snapshot.
func BuildCatalog(ctx context.Context, count CountFunc, recs []Record) (CatalogSection, error) {
	var list []Record
	for _, r := range recs {
		if r.Loadable() {
			list = append(list, r)
		}
	}
	if len(list) == 0 {
		return CatalogSection{}, nil
	}
	slices.SortFunc(list, func(a, b Record) int { return strings.Compare(a.Name, b.Name) })
	try := func(k int) (string, int, bool, error) {
		var sb strings.Builder
		sb.WriteString(CatalogHeading + "\n")
		for _, r := range list[:k] {
			fmt.Fprintf(&sb, "- %s: %s\n", r.Name, strings.Join(strings.Fields(r.Description), " "))
		}
		text := sb.String()
		if k < len(list) {
			text += fmt.Sprintf("(%d more skills; skill_use with a query finds them)\n", len(list)-k)
		}
		n, exact, err := count(ctx, text)
		if err != nil {
			return "", 0, false, fmt.Errorf("count skills catalog: %w", err)
		}
		if !exact {
			return "", 0, false, ErrNotExact
		}
		return text, n, n <= CatalogBudgetTokens, nil
	}
	best, tokens, ok, err := try(0)
	if err != nil {
		return CatalogSection{}, err
	}
	if !ok {
		return CatalogSection{}, fmt.Errorf("%w: the heading alone exceeds it", ErrCatalogOverBudget)
	}
	lo, hi := 0, len(list)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		text, n, ok, err := try(mid)
		if err != nil {
			return CatalogSection{}, err
		}
		if ok {
			best, tokens, lo = text, n, mid
		} else {
			hi = mid - 1
		}
	}
	return CatalogSection{Text: best, Tokens: tokens, Listed: lo, Overflow: len(list) - lo}, nil
}
