package provider_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

func TestContextOverflow(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"prompt is too long: 213462 tokens > 200000 maximum", true},
		{"This model's maximum context length is 131072 tokens. However, you requested 140000 tokens", true},
		{"Your input exceeds the context window of this model", true},
		{"input token count (1100000) exceeds the maximum number of tokens allowed (1048576)", true},
		{"the request exceeds the available context size, try increasing it", true},
		{"prompt has 70,000 tokens, but the configured context size is 65,536 tokens", true},
		{"context_length_exceeded", true},
		{"Too many tokens, please wait before trying again.", false},
		{"Throttling error: Too many tokens, please wait before trying again.", false},
		{"Rate limit reached: too many tokens per minute", false},
		{"Too many requests", false},
		{"invalid api key", false},
	}
	for _, c := range cases {
		err := &provider.Error{Op: "stream chat completion", StatusCode: 400, Err: errors.New(c.text)}
		if got := provider.ContextOverflow(fmt.Errorf("call model: %w", err)); got != c.want {
			t.Errorf("ContextOverflow(%q) = %t, want %t", c.text, got, c.want)
		}
	}
	if provider.ContextOverflow(nil) {
		t.Error("ContextOverflow(nil) = true")
	}
}

func TestStripReasoning(t *testing.T) {
	cases := map[string]string{
		"<think>weighing it</think>\nThe answer": "\nThe answer",
		"  <think>a\nb</think>answer":            "answer",
		"answer <think>kept</think>":             "answer <think>kept</think>",
		"no reasoning":                           "no reasoning",
	}
	for in, want := range cases {
		if got := provider.StripReasoning(in); got != want {
			t.Errorf("StripReasoning(%q) = %q, want %q", in, got, want)
		}
	}
}
