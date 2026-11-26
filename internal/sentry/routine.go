package sentry

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/sentry/scan"
)

// RoutineSpan locates a screened window in the stored prompt in bytes.
type RoutineSpan struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
}

// RoutineWarning contains screen results without repeating the prompt.
type RoutineWarning struct {
	Spans     []RoutineSpan `json:"spans"`
	MaxScore  float64       `json:"max_score"`
	Unscanned bool          `json:"unscanned"`
}

// RoutineChecker screens a routine prompt without blocking on a hit.
type RoutineChecker interface {
	CheckRoutine(context.Context, string) (RoutineWarning, error)
}

// CheckRoutine screens the prompt and returns byte offsets into the original text.
func (g *gate) CheckRoutine(ctx context.Context, prompt string) (RoutineWarning, error) {
	threshold := g.Threshold
	if g.ScreenSettings != nil {
		var err error
		threshold, _, err = g.ScreenSettings(ctx)
		if err != nil {
			return RoutineWarning{}, fmt.Errorf("read screen settings: %w", err)
		}
	}
	if g.MemoryThreshold != nil {
		var err error
		threshold, err = g.MemoryThreshold(ctx)
		if err != nil {
			return RoutineWarning{}, fmt.Errorf("read memory threshold: %w", err)
		}
	}
	clean, offsets := scan.StripInvisibleMap(strings.ToValidUTF8(prompt, "�"))
	windows, unscanned := g.score(ctx, []Segment{{Source: Source{Kind: SourceKind("routine")}}}, []string{clean})
	if err := ctx.Err(); err != nil {
		return RoutineWarning{}, err
	}
	result := RoutineWarning{Spans: []RoutineSpan{}, Unscanned: unscanned}
	for _, group := range windows {
		for _, w := range group {
			if w.Score < threshold {
				continue
			}
			start, end := runeStart(clean, max(w.Start, 0)), runeStart(clean, min(w.End, len(clean)))
			if start >= end {
				continue
			}
			a, b := offsets[start], offsets[end-1]
			_, n := utf8.DecodeRuneInString(prompt[b:])
			result.Spans = append(result.Spans, RoutineSpan{Start: a, End: b + n, Score: w.Score})
			result.MaxScore = max(result.MaxScore, w.Score)
		}
	}
	return result, nil
}
