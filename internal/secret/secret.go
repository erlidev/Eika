package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeySize is the length of a Box key in bytes.
const KeySize = 32

// ErrCorrupt reports sealed data that does not open with the box's key: it
// was damaged, or it was sealed under a key the harness no longer has.
var ErrCorrupt = errors.New("sealed data does not open with this key")

// Box seals and opens short secrets with one key. It is safe for concurrent
// use.
type Box struct {
	aead cipher.AEAD
}

// New returns a Box on a KeySize-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("build secret box: key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build secret box: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build secret box: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Load returns a Box on the hex-encoded key stored at path. A file that does
// not exist yet is created with a fresh random key, readable by the harness
// user alone, which is what happens on a deployment's first start.
func Load(path string) (*Box, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return create(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read secret key %s: %w", path, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("read secret key %s: not hex: %w", path, err)
	}
	return New(key)
}

// create writes a new random key to path and returns a Box on it. The file is
// opened exclusively, so two harnesses starting on one volume cannot each
// write a key; the one that loses reads the other's.
func create(path string) (*Box, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return Load(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create secret key %s: %w", path, err)
	}
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write secret key %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("write secret key %s: %w", path, err)
	}
	return New(key)
}

// Seal encrypts plaintext under a fresh nonce. The empty string seals to
// nothing, so a row can say "no credential" without a ciphertext.
func (b *Box) Seal(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plaintext)+b.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal secret: %w", err)
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts what Seal produced. Nothing opens to the empty string; data
// that does not authenticate is ErrCorrupt.
func (b *Box) Open(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	size := b.aead.NonceSize()
	if len(sealed) < size+b.aead.Overhead() {
		return "", fmt.Errorf("open secret: %w", ErrCorrupt)
	}
	plain, err := b.aead.Open(nil, sealed[:size], sealed[size:], nil)
	if err != nil {
		return "", fmt.Errorf("open secret: %w", ErrCorrupt)
	}
	return string(plain), nil
}
