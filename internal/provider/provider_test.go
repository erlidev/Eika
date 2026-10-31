package provider_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

func TestRegistryBuild(t *testing.T) {
	built := 0
	chat := func(e provider.Endpoint) (provider.Provider, error) {
		built++
		if e.BaseURL == "broken" {
			return nil, errors.New("no key")
		}
		return nil, nil
	}
	r := provider.NewRegistry(chat, func(provider.Endpoint) (provider.Provider, error) {
		return nil, errors.New("responses")
	})

	if _, err := r.Build("openai", provider.Endpoint{BaseURL: "http://api"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built != 1 {
		t.Errorf("constructor called %d times, want 1", built)
	}
	if _, err := r.Build("openai", provider.Endpoint{BaseURL: "http://api", API: provider.APIChat}); err != nil || built != 2 {
		t.Errorf("Build with the chat api = %v after %d calls, want the chat constructor", err, built)
	}
	if _, err := r.Build("openai", provider.Endpoint{BaseURL: "http://api", API: provider.APIResponses}); err == nil || err.Error() != "responses" {
		t.Errorf("Build with the responses api = %v, want the responses constructor", err)
	}
	if _, err := r.Build("openai", provider.Endpoint{BaseURL: "http://api", API: "soap"}); err == nil {
		t.Error("Build of an unknown api returned no error")
	}
	chatOnly := provider.NewRegistry(chat, nil)
	if _, err := chatOnly.Build("openai", provider.Endpoint{BaseURL: "http://api", API: provider.APIResponses}); err == nil ||
		!strings.Contains(err.Error(), "does not speak the responses api") {
		t.Errorf("Build of a protocol the kind does not speak = %v, want an error saying so", err)
	}
	if _, err := r.Build("openai", provider.Endpoint{BaseURL: "broken"}); err == nil {
		t.Error("Build of a broken endpoint returned no error")
	}
	if _, err := r.Build("anthropic", provider.Endpoint{}); err == nil {
		t.Error("Build of an unknown kind returned no error")
	}
	if kinds := r.Kinds(); len(kinds) != 1 || kinds[0] != "openai" {
		t.Errorf("Kinds = %v, want [openai]", kinds)
	}
}

func TestValidReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max", "HIGH", "think-harder", "ultra_2"} {
		if !provider.ValidReasoningEffort(effort) {
			t.Errorf("ValidReasoningEffort(%q) = false", effort)
		}
	}
	for _, effort := range []string{" low", "very high", "high!", strings.Repeat("x", provider.MaxReasoningEffortLen+1)} {
		if provider.ValidReasoningEffort(effort) {
			t.Errorf("ValidReasoningEffort(%q) = true", effort)
		}
	}
}

func TestValidThinkingSwitch(t *testing.T) {
	for _, s := range []provider.ThinkingSwitch{"", provider.SwitchReasoningEffort, provider.SwitchTemplate, provider.SwitchThinking} {
		if !provider.ValidThinkingSwitch(s) {
			t.Errorf("ValidThinkingSwitch(%q) = false", s)
		}
	}
	for _, s := range []provider.ThinkingSwitch{"enable_thinking", "Thinking", " thinking"} {
		if provider.ValidThinkingSwitch(s) {
			t.Errorf("ValidThinkingSwitch(%q) = true", s)
		}
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

func TestToolCallIDsSurviveStorageAndOldEntriesHaveOne(t *testing.T) {
	none, wire := "", "call_0"
	for _, tc := range []struct {
		name     string
		stored   string
		wantWire *string
		wantSent string
	}{
		{"an entry stored before ids were minted", `{"id":"toolu_1","name":"ls","arguments":{}}`, nil, "toolu_1"},
		{"a call the backend named", `{"id":"call_ab","wire_id":"call_0","name":"ls","arguments":{}}`, &wire, "call_0"},
		{"a call the backend gave no id", `{"id":"call_ab","wire_id":"","name":"ls","arguments":{}}`, &none, "call_ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c provider.ToolCall
			if err := json.Unmarshal([]byte(tc.stored), &c); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.WireID, tc.wantWire) {
				t.Errorf("WireID = %v, want %v", c.WireID, tc.wantWire)
			}
			if got := c.Wire(); got != tc.wantSent {
				t.Errorf("Wire() = %q, want %q", got, tc.wantSent)
			}
			again, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != tc.stored {
				t.Errorf("stored again as %s, want it unchanged: %s", again, tc.stored)
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
