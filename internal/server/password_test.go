package server

import (
	"strings"
	"testing"
)

func TestPasswordHashesCheckTheirOwnPasswordOnly(t *testing.T) {
	stored, err := hashPassword("correct horse")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if strings.Contains(stored, "correct horse") {
		t.Errorf("stored hash carries the password: %s", stored)
	}
	if !checkPassword(stored, "correct horse") {
		t.Error("the password does not match its own hash")
	}
	for _, wrong := range []string{"", "correct hors", "correct horse ", "Correct horse"} {
		if checkPassword(stored, wrong) {
			t.Errorf("%q matched", wrong)
		}
	}
	again, _ := hashPassword("correct horse")
	if again == stored {
		t.Error("two hashes of one password are equal; the salt is not fresh")
	}
}

func TestCheckPasswordRefusesAHashItCannotRead(t *testing.T) {
	for _, stored := range []string{
		"",
		"md5$1$c2FsdA$a2V5",
		"pbkdf2-sha256$0$c2FsdA$a2V5",
		"pbkdf2-sha256$1$c2FsdA$",
	} {
		if checkPassword(stored, "anything") {
			t.Errorf("checkPassword(%q) matched", stored)
		}
	}
}

func TestSessionTokensAreStoredOnlyAsTheirHash(t *testing.T) {
	token, hash, err := newSessionToken()
	if err != nil {
		t.Fatalf("newSessionToken: %v", err)
	}
	if token == hash || strings.Contains(hash, token) {
		t.Errorf("hash %q reveals token %q", hash, token)
	}
	if tokenHash(token) != hash {
		t.Error("tokenHash does not reproduce the stored hash")
	}
	other, _, _ := newSessionToken()
	if other == token {
		t.Error("two tokens are equal")
	}
}
