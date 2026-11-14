package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestInstructionTextValidation(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{
		{"empty", "", true}, {"markdown", "# Rules\nUse metric.\n", true},
		{"limit", strings.Repeat("a", 65536), true}, {"over limit", strings.Repeat("a", 65537), false},
		{"multibyte", strings.Repeat("é", 32769), false}, {"NUL", "a\x00b", false}, {"invalid UTF-8", string([]byte{0xff}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.ValidateInstructionText(tc.text)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, store.ErrInvalidInstructions) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
