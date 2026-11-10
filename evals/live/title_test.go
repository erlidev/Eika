package live_test

import (
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

func TestSessionTitleAcceptsAShortTitle(t *testing.T) {
	cs := newChatServer(t, reply{prompt: 60, completion: 6, text: `"Fix the login loop."`})
	res := live.SessionTitle().Execute(context.Background(), connect(t, cs.URL))
	if !res.Passed {
		t.Fatalf("failed: %s", res.Error)
	}
	if len(cs.requests) != 1 {
		t.Fatalf("requests = %d, want the one title request", len(cs.requests))
	}
	body := cs.requests[0]
	if tools, ok := body["tools"]; ok && tools != nil {
		t.Errorf("the title request offered tools: %v", tools)
	}
	if kwargs, _ := body["chat_template_kwargs"].(map[string]any); kwargs["enable_thinking"] != false {
		t.Errorf("chat_template_kwargs = %v, want thinking off through the template", body["chat_template_kwargs"])
	}
}

func TestSessionTitleFailsOnAnAnswer(t *testing.T) {
	cs := newChatServer(t, reply{prompt: 60, completion: 40, text: strings.Repeat("It loops because the redirect never ends ", 3)})
	res := live.SessionTitle().Execute(context.Background(), connect(t, cs.URL))
	if res.Passed || !strings.Contains(res.Error, "answered the message") {
		t.Fatalf("passed = %v, error = %q", res.Passed, res.Error)
	}
}
