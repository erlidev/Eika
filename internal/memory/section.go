package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// SectionBudgetTokens is the exact token budget of the memory section,
	// labels and overflow count included.
	SectionBudgetTokens = 3000
	// MaxPins is the most pinned memories the section carries.
	MaxPins = 12
)

var (
	// ErrTooManyPins rejects a pin that would make more than MaxPins.
	ErrTooManyPins = errors.New("too many pinned memories")
	// ErrPinsOverBudget rejects a pin or edit that would put the pinned
	// section over SectionBudgetTokens.
	ErrPinsOverBudget = errors.New("pinned memories exceed the token budget")
	// ErrNotExact is returned when the counter did not count exactly.
	ErrNotExact = errors.New("token count is not exact")
)

// CountFunc counts the tokens of text with the model's own counter. exact is
// false when the count is an estimate; the section is never built from one.
// The caller adapts the existing counter (internal/provider).
type CountFunc func(ctx context.Context, text string) (tokens int, exact bool, err error)

// Section is a built memory section.
type Section struct {
	Text     string
	Tokens   int
	Pinned   int // pinned memories rendered
	Indexed  int // index lines rendered
	Overflow int // eligible memories that did not fit
}

// eligible reports whether f may auto-load: active and not past its expiry.
func eligible(f File, now time.Time) bool {
	if f.State != "active" {
		return false
	}
	return f.Expires == "" || now.UTC().Format("2006-01-02") <= f.Expires
}

// priority orders by recent update, newest first, with name as tie-breaker.
// Timestamps are compared as parsed times, falling back to the text.
func priority(list []File) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		ta, ea := time.Parse(time.RFC3339, a.Updated)
		tb, eb := time.Parse(time.RFC3339, b.Updated)
		if ea == nil && eb == nil && !ta.Equal(tb) {
			return ta.After(tb)
		}
		if (ea != nil || eb != nil) && a.Updated != b.Updated {
			return a.Updated > b.Updated
		}
		return a.Name < b.Name
	})
}

func byTypeName(list []File) []File {
	out := append([]File(nil), list...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// render lays out whole memories only; nothing is ever cut.
func render(pins, index []File, overflow int) string {
	var b strings.Builder
	b.WriteString("## Memory\n")
	if len(pins) > 0 {
		b.WriteString("Pinned:\n")
		for _, f := range byTypeName(pins) {
			fmt.Fprintf(&b, "[%s] (%s)\n%s\n", f.Name, f.Type, strings.TrimRight(f.Body, "\n"))
		}
	}
	if len(index) > 0 {
		b.WriteString("Index (memory_read <name> for details, memory_search for anything not listed):\n")
		for _, f := range byTypeName(index) {
			fmt.Fprintf(&b, "- %s: %s\n", f.Name, f.Description)
		}
	}
	if overflow > 0 {
		fmt.Fprintf(&b, "(%d more memories; search to find them)\n", overflow)
	}
	return b.String()
}

func count(ctx context.Context, c CountFunc, text string) (int, error) {
	n, exact, err := c(ctx, text)
	if err != nil {
		return 0, fmt.Errorf("count memory section: %w", err)
	}
	if !exact {
		return 0, ErrNotExact
	}
	return n, nil
}

// BuildSection renders the memory section within SectionBudgetTokens. Active,
// unexpired pinned memories come first (at most MaxPins), then the most
// recently updated, name breaking ties; whole entries only, and what did not
// fit is counted in the section. A counting failure is returned and the
// caller keeps its previous snapshot. The count is exact or the build fails.
func BuildSection(ctx context.Context, c CountFunc, memories []File, now time.Time) (Section, error) {
	var pins, rest []File
	for _, f := range memories {
		switch {
		case !eligible(f, now):
		case f.Pinned:
			pins = append(pins, f)
		default:
			rest = append(rest, f)
		}
	}
	priority(pins)
	priority(rest)
	if len(pins) > MaxPins {
		rest = append(pins[MaxPins:], rest...) // excess pins fall to the index
		pins = pins[:MaxPins]
		priority(rest)
	}
	order := append(append([]File(nil), pins...), rest...)
	total := len(order)

	build := func(k int) (string, int) {
		p := min(k, len(pins))
		return render(order[:p], order[p:k], total-k), p
	}
	// Largest prefix that fits; size grows with k, so bisect.
	lo, hi := 0, total
	best, err := fit(ctx, c, build, 0)
	if err != nil {
		return Section{}, err
	}
	if !best.ok {
		// Not even the empty section fits: nothing can be listed.
		return Section{}, fmt.Errorf("memory section labels exceed the token budget")
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		r, err := fit(ctx, c, build, mid)
		if err != nil {
			return Section{}, err
		}
		if r.ok {
			best, lo = r, mid
		} else {
			hi = mid - 1
		}
	}
	p := min(lo, len(pins))
	return Section{Text: best.text, Tokens: best.tokens, Pinned: p, Indexed: lo - p, Overflow: total - lo}, nil
}

type fitResult struct {
	text   string
	tokens int
	ok     bool
}

func fit(ctx context.Context, c CountFunc, build func(int) (string, int), k int) (fitResult, error) {
	text, _ := build(k)
	n, err := count(ctx, c, text)
	if err != nil {
		return fitResult{}, err
	}
	return fitResult{text: text, tokens: n, ok: n <= SectionBudgetTokens}, nil
}

// CheckPinBudget is the validation a pin or edit passes before it is written.
// active is every stored memory; next is the memory as it would be stored. If
// next would be a pinned, active, unexpired memory, the pinned set with it
// must have at most MaxPins entries and render within SectionBudgetTokens
// (labels included, pins only). Anything else passes without counting.
func CheckPinBudget(ctx context.Context, c CountFunc, active []File, next File, now time.Time) error {
	var pins []File
	for _, f := range active {
		if f.Name != next.Name && f.Pinned && eligible(f, now) {
			pins = append(pins, f)
		}
	}
	if !next.Pinned || !eligible(next, now) {
		return nil
	}
	pins = append(pins, next)
	if len(pins) > MaxPins {
		return fmt.Errorf("%w: limit is %d", ErrTooManyPins, MaxPins)
	}
	n, err := count(ctx, c, render(pins, nil, 0))
	if err != nil {
		return err
	}
	if n > SectionBudgetTokens {
		return fmt.Errorf("%w: %d of %d tokens", ErrPinsOverBudget, n, SectionBudgetTokens)
	}
	return nil
}
