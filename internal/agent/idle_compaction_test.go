package agent

import "testing"

func TestIdleFillBoundary(t *testing.T) {
	for _, tc := range []struct {
		tokens int
		want   bool
	}{{599, false}, {600, false}, {601, true}} {
		if got := exceedsIdleFill(tc.tokens, 1000, 60); got != tc.want {
			t.Errorf("fill at %d tokens = %v", tc.tokens, got)
		}
	}
}
