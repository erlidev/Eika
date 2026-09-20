package server

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Password hashing parameters: PBKDF2 with HMAC-SHA256 at the iteration count
// OWASP recommends for it. The count is part of every stored hash, so raising
// it later leaves the hashes already stored checkable.
const (
	passwordScheme     = "pbkdf2-sha256"
	passwordIterations = 600_000
	passwordSaltBytes  = 16
	passwordKeyBytes   = 32
	// minPasswordLength is the shortest password setup accepts.
	minPasswordLength = 8
	// maxPasswordLength bounds the work one sign-in attempt can ask for.
	maxPasswordLength = 1024
)

// sessionTokenBytes is the randomness of a sign-in session token.
const sessionTokenBytes = 32

// hashPassword derives the stored form of a password under a fresh salt:
// scheme, iterations, salt, and key, separated by dollar signs.
func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyBytes)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	enc := base64.RawStdEncoding
	return strings.Join([]string{
		passwordScheme, strconv.Itoa(passwordIterations), enc.EncodeToString(salt), enc.EncodeToString(key),
	}, "$"), nil
}

// checkPassword reports whether password matches a hash hashPassword made.
// A hash it cannot read matches nothing.
func checkPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != passwordScheme {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// validatePassword rejects a password too short to protect the harness, or
// too long to hash cheaply.
func validatePassword(password string) error {
	switch {
	case len([]rune(password)) < minPasswordLength:
		return invalidf("the password must be at least %d characters", minPasswordLength)
	case len(password) > maxPasswordLength:
		return invalidf("the password must be at most %d bytes", maxPasswordLength)
	}
	return nil
}

// newSessionToken returns a fresh sign-in token and the hash it is stored
// under. Only the hash reaches the database, so a copy of it cannot be used
// to sign in.
func newSessionToken() (token, hash string, err error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("create session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, tokenHash(token), nil
}

// tokenHash is the form a session token is stored and looked up under.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
