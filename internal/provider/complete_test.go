package provider_test

import (
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

func TestComplete(t *testing.T) {
	failure := errors.New("overloaded")
	cases := []struct {
		name     string
		step     providertest.Step
		wantText string
		wantStop string
		wantErr  bool
	}{
		{"text and a stop reason", providertest.Stream(provider.TextDelta("re"), provider.TextDelta("ady"), provider.Done("stop")), "ready", "stop", false},
		{"a failed call", providertest.Fail(failure), "", "", true},
		{"a failure mid-stream", providertest.Stream(provider.TextDelta("re"), provider.Errorf(failure)), "", "", true},
		{"a stream that never finishes", providertest.Stream(provider.TextDelta("re")), "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, stop, err := provider.Complete(t.Context(), providertest.New(tc.step), provider.Request{Model: "m"})
			if (err != nil) != tc.wantErr || text != tc.wantText || stop != tc.wantStop {
				t.Errorf("Complete = %q, %q, %v", text, stop, err)
			}
		})
	}
}
