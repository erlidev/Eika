package skill

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

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
// refuses a catalog past the budget, so overflow is the edge a changed counter
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
		text := Catalog(list[:k])
		if k == 0 {
			text = CatalogHeading + "\n"
		}
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
		return CatalogSection{}, fmt.Errorf("skills catalog heading exceeds the token budget")
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

// CatalogHeading is the first line of the catalog section.
const CatalogHeading = "## Skills (skill_use <name> to load one)"
