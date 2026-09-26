package provider

import (
	"context"
	"errors"
	"strings"
)

// Complete runs one request to the end and returns its text and stop reason.
// It is for a caller that wants a whole answer rather than a stream: a model
// test, or a utility task.
func Complete(ctx context.Context, p Provider, req Request) (string, string, error) {
	events, err := p.Stream(ctx, req)
	if err != nil {
		return "", "", err
	}
	var text strings.Builder
	for e := range events {
		switch e.Kind {
		case KindTextDelta:
			text.WriteString(e.Text)
		case KindError:
			return "", "", e.Err
		case KindDone:
			return text.String(), e.StopReason, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	return "", "", errors.New("the response ended without finishing")
}
