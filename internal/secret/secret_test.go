package secret_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/secret"
)

// newBox returns a box on a key made for the test.
func newBox(t *testing.T) *secret.Box {
	t.Helper()
	b, err := secret.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return b
}

func TestSealOpensToWhatWasSealed(t *testing.T) {
	b := newBox(t)
	for _, plain := range []string{"sk-proj-abc123", "x", "ünïcödé 🔑"} {
		sealed, err := b.Seal(plain)
		if err != nil {
			t.Fatalf("Seal(%q): %v", plain, err)
		}
		// A one-byte plaintext turns up in random ciphertext by chance, so
		// only a plaintext long enough not to is looked for.
		if len(plain) >= 8 && bytes.Contains(sealed, []byte(plain)) {
			t.Errorf("sealed data carries the plaintext %q", plain)
		}
		got, err := b.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != plain {
			t.Errorf("Open = %q, want %q", got, plain)
		}
	}
}

func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	b := newBox(t)
	first, _ := b.Seal("same")
	second, _ := b.Seal("same")
	if bytes.Equal(first, second) {
		t.Error("sealing the same secret twice gave the same bytes")
	}
}

func TestTheEmptySecretSealsToNothing(t *testing.T) {
	b := newBox(t)
	sealed, err := b.Seal("")
	if err != nil || sealed != nil {
		t.Fatalf("Seal(\"\") = %v, %v; want nil, nil", sealed, err)
	}
	if got, err := b.Open(nil); err != nil || got != "" {
		t.Errorf("Open(nil) = %q, %v; want empty", got, err)
	}
}

func TestOpenRejectsDataItDidNotSeal(t *testing.T) {
	b := newBox(t)
	sealed, _ := b.Seal("sk-secret")
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	other := newBox(t)

	cases := map[string]struct {
		box  *secret.Box
		data []byte
	}{
		"a flipped bit":   {b, tampered},
		"too short":       {b, sealed[:4]},
		"a different key": {other, sealed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.box.Open(c.data); !errors.Is(err, secret.ErrCorrupt) {
				t.Errorf("Open = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestLoadCreatesTheKeyOnceAndReadsItBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "secret.key")
	first, err := secret.Load(path)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, want 0600", info.Mode().Perm())
	}
	sealed, _ := first.Seal("kept across restarts")

	second, err := secret.Load(path)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got, err := second.Open(sealed); err != nil || got != "kept across restarts" {
		t.Errorf("Open after reload = %q, %v", got, err)
	}
}

func TestLoadRejectsAKeyFileThatIsNotAKey(t *testing.T) {
	for name, body := range map[string]string{
		"not hex":   "not a key",
		"too short": "abcd\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret.key")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write key: %v", err)
			}
			if _, err := secret.Load(path); err == nil {
				t.Error("Load accepted a malformed key file")
			}
		})
	}
}
