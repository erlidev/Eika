package live

import (
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

func TestValidateCachedTokens(t *testing.T) {
	prompt := 100
	for _, tt := range []struct {
		name   string
		cached int
		valid  bool
	}{
		{"negative", -1, false},
		{"zero", 0, true},
		{"equal to prompt", 100, true},
		{"above prompt", 101, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := Call{PromptTokens: &prompt, CachedTokens: &tt.cached}
			if err := validateCachedTokens(c); (err == nil) != tt.valid {
				t.Fatalf("validate cached tokens: %v", err)
			}
		})
	}
}

func TestLearnedImageRequiresAnchoredMethod(t *testing.T) {
	zero := 0
	c := Call{Method: string(provider.MethodEstimated), Drift: &zero}
	if err := requireImageMethod(c, true); err == nil {
		t.Fatal("estimated image prediction with zero drift passed")
	}
	c.Method = string(provider.MethodAnchored)
	if err := requireImageMethod(c, true); err != nil {
		t.Fatal(err)
	}
}
