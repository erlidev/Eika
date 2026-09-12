package provider_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/provider"
)

func TestRegistryBuild(t *testing.T) {
	built := 0
	r := provider.NewRegistry(func(m config.Model) (provider.Provider, error) {
		built++
		if m.Name == "broken" {
			return nil, errors.New("no key")
		}
		return nil, nil
	})

	if _, err := r.Build("openai", config.Model{Name: "gpt"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built != 1 {
		t.Errorf("constructor called %d times, want 1", built)
	}
	if _, err := r.Build("openai", config.Model{Name: "broken"}); err == nil {
		t.Error("Build of a broken model returned no error")
	}
	if _, err := r.Build("anthropic", config.Model{Name: "claude"}); err == nil {
		t.Error("Build of an unknown kind returned no error")
	}
	if kinds := r.Kinds(); len(kinds) != 1 || kinds[0] != "openai" {
		t.Errorf("Kinds = %v, want [openai]", kinds)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantRetryable bool
		wantAfter     time.Duration
	}{
		{"plain error", errors.New("boom"), false, 0},
		{"provider error", &provider.Error{Op: "stream", Err: errors.New("boom")}, false, 0},
		{
			"retryable provider error",
			&provider.Error{Op: "stream", StatusCode: 429, Retryable: true, RetryAfter: time.Second, Err: errors.New("slow down")},
			true,
			time.Second,
		},
		{
			"wrapped retryable error",
			fmt.Errorf("run turn: %w", &provider.Error{Op: "stream", Retryable: true, Err: errors.New("boom")}),
			true,
			0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := provider.Retryable(c.err); got != c.wantRetryable {
				t.Errorf("Retryable = %v, want %v", got, c.wantRetryable)
			}
			if got := provider.RetryAfter(c.err); got != c.wantAfter {
				t.Errorf("RetryAfter = %v, want %v", got, c.wantAfter)
			}
		})
	}
}

func TestErrorMessage(t *testing.T) {
	withStatus := &provider.Error{Op: "stream chat completion", StatusCode: 500, Err: errors.New("boom")}
	if got := withStatus.Error(); got != "stream chat completion: http 500: boom" {
		t.Errorf("Error = %q", got)
	}
	withoutStatus := &provider.Error{Op: "stream chat completion", Err: errors.New("boom")}
	if got := withoutStatus.Error(); got != "stream chat completion: boom" {
		t.Errorf("Error = %q", got)
	}
	if !errors.Is(fmt.Errorf("x: %w", withStatus), withStatus) {
		t.Error("provider.Error does not survive wrapping")
	}
}
