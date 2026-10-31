package agent

import (
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

func assistantWith(context provider.Usage) provider.Message {
	m := provider.AssistantMessage("answer", nil)
	m.Metrics = &provider.MessageMetrics{RunID: "r", Context: context}
	return m
}

func TestLastMeasuredReadsOnlyWhatTheEndpointStated(t *testing.T) {
	rep := provider.Reported
	cases := []struct {
		name    string
		msgs    []provider.Message
		wantFit int
		wantAt  int
	}{
		{"the total", []provider.Message{provider.UserMessage("q"), assistantWith(provider.Usage{TotalTokens: rep(120)})}, 120, 1},
		{"prompt and response when no total", []provider.Message{provider.UserMessage("q"), assistantWith(provider.Usage{InputTokens: rep(100), OutputTokens: rep(20)})}, 120, 1},
		{"a prompt count alone is not the fill", []provider.Message{provider.UserMessage("q"), assistantWith(provider.Usage{InputTokens: rep(100)})}, 0, -1},
		{"nothing stated", []provider.Message{provider.UserMessage("q"), assistantWith(provider.Usage{})}, 0, -1},
		{"an older call that stated its fill answers for one that did not",
			[]provider.Message{assistantWith(provider.Usage{TotalTokens: rep(50)}), provider.UserMessage("q"), assistantWith(provider.Usage{})}, 50, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fit, at := lastMeasured(c.msgs)
			if fit != c.wantFit || at != c.wantAt {
				t.Errorf("lastMeasured = %d at %d, want %d at %d", fit, at, c.wantFit, c.wantAt)
			}
		})
	}
}

func TestSilentOverflowNeedsTheStatedPromptToFillTheWindow(t *testing.T) {
	rep := provider.Reported
	empty := provider.AssistantMessage("", nil)
	cases := []struct {
		name string
		resp response
		want bool
	}{
		{"prompt fills the window and nothing came back", response{message: empty, stop: "length", usage: provider.Usage{InputTokens: rep(1000), OutputTokens: rep(0)}}, true},
		{"output count not stated, nothing came back", response{message: empty, stop: "length", usage: provider.Usage{InputTokens: rep(1000)}}, true},
		{"prompt count not stated", response{message: empty, stop: "length", usage: provider.Usage{OutputTokens: rep(0)}}, false},
		{"something was generated", response{message: empty, stop: "length", usage: provider.Usage{InputTokens: rep(1000), OutputTokens: rep(5)}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := silentOverflow(c.resp, 1000); got != c.want {
				t.Errorf("silentOverflow = %v, want %v", got, c.want)
			}
		})
	}
}
