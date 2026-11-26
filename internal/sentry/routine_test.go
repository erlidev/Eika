package sentry_test

import (
	"context"
	"testing"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/sentry/screen"
)

func TestRoutineScreenReportsOriginalOffsets(t *testing.T) {
	checker := sentry.New(sentry.Options{Screen: fixedWindows([]screen.Window{{Start: 0, End: 2, Score: 0.99}}), Threshold: 0.9}).(sentry.RoutineChecker)
	prompt := "a\u200bb"
	result, err := checker.CheckRoutine(context.Background(), prompt)
	if err != nil || result.Unscanned || len(result.Spans) != 1 || prompt[result.Spans[0].Start:result.Spans[0].End] != prompt {
		t.Fatalf("screen = %+v, %v", result, err)
	}
}
