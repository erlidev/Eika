package agent

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

func TestHomeContextIncludesWebChannel(t *testing.T) {
	a := New(nil, nil, Options{Channel: "web"})
	m := a.stamp(provider.UserMessage("hello"))
	if !strings.Contains(m.Context, "\nchannel: web\n") {
		t.Fatalf("context = %q", m.Context)
	}
	if a.stamp(m).Context != m.Context {
		t.Fatal("stamping changed existing context")
	}
}
