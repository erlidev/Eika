// Package scan holds the deterministic steps of ingest: removing characters
// the reader cannot see, and redacting secrets by pattern. It depends on
// nothing in Eika. Design: docs/design/sentry.md, section 6.
package scan

import (
	"regexp"
	"strings"
)

// invisible reports whether r is a character that hides text from a reader
// while a model still sees it: Unicode tag characters, zero-width characters
// and bidirectional controls.
func invisible(r rune) bool {
	switch {
	case r >= 0xE0000 && r <= 0xE007F: // tag characters
		return true
	case r >= 0x200B && r <= 0x200F: // zero-width space, joiners, direction marks
		return true
	case r >= 0x202A && r <= 0x202E: // bidirectional embeddings and overrides
		return true
	case r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069: // word joiner, invisible operators, isolates
		return true
	}
	return r == 0xFEFF || r == 0x061C
}

// StripInvisible removes the invisible characters from text.
func StripInvisible(text string) string {
	if !strings.ContainsFunc(text, invisible) {
		return text
	}
	return strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, text)
}

// secret is one well-known key format.
type secret struct {
	kind string
	re   *regexp.Regexp
}

// secrets are tried in order, so a format that contains another comes first.
var secrets = []secret{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{32,}`)},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{50,})`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}\b`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abeoprs]-[A-Za-z0-9-]{10,}`)},
	{"stripe-key", regexp.MustCompile(`\b[rs]k_live_[A-Za-z0-9]{20,}`)},
	{"npm-token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// Redact replaces each secret in text with [redacted:<kind>].
func Redact(text string) string {
	for _, s := range secrets {
		text = s.re.ReplaceAllString(text, "[redacted:"+s.kind+"]")
	}
	return text
}
