package sentry

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/erlidev/eika/internal/sentry/scan"
)

// maxUnescape bounds how many layers of percent-encoding are undone.
const maxUnescape = 3

// outbound holds the arguments that leave Eika in a call: the text a
// destination can read. It returns nil for an action that is not checked.
// bash and file writes are never checked, since a command may hold a key the
// user gave it. Phase 11 adds the string arguments of a plugin call here.
func outbound(a Action) []string {
	if a.Kind != KindTool {
		return nil // KindPlugin: phase 11
	}
	switch a.Name {
	case "web_fetch":
		return []string{stringArg(a.Args, "url")}
	case "web_search":
		return []string{stringArg(a.Args, "query")}
	}
	return nil
}

// stringArg returns the string member key of a JSON object, or "" when there
// is none or the arguments do not parse. Like json.Unmarshal into a struct it
// matches the key without regard to case and takes the last of a repeated key.
// Arguments that are a flat object of plain strings are read in one pass;
// anything else (an escape, a non-ASCII byte, another type) goes to
// encoding/json, which would otherwise scan 64 KiB twice.
func stringArg(raw json.RawMessage, key string) string {
	if v, ok := plainStringMember(raw, key); ok {
		return v
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	var out string
	for k, v := range args {
		if strings.EqualFold(k, key) {
			var s string
			if json.Unmarshal(v, &s) == nil {
				out = s
			}
		}
	}
	return out
}

// plainStringMember reads a flat JSON object whose keys and values are strings
// of printable ASCII with no escapes. ok is false for anything else.
func plainStringMember(raw []byte, key string) (value string, ok bool) {
	i := skipSpace(raw, 0)
	if i >= len(raw) || raw[i] != '{' {
		return "", false
	}
	i = skipSpace(raw, i+1)
	if i < len(raw) && raw[i] == '}' {
		return "", skipSpace(raw, i+1) == len(raw)
	}
	for {
		k, next, ok := plainString(raw, i)
		if !ok {
			return "", false
		}
		i = skipSpace(raw, next)
		if i >= len(raw) || raw[i] != ':' {
			return "", false
		}
		v, next, ok := plainString(raw, skipSpace(raw, i+1))
		if !ok {
			return "", false
		}
		if strings.EqualFold(k, key) {
			value = v
		}
		i = skipSpace(raw, next)
		if i >= len(raw) {
			return "", false
		}
		switch raw[i] {
		case '}':
			return value, skipSpace(raw, i+1) == len(raw)
		case ',':
			i = skipSpace(raw, i+1)
		default:
			return "", false
		}
	}
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// plainString reads the string that starts at b[i], returning the index after it.
func plainString(b []byte, i int) (s string, next int, ok bool) {
	if i >= len(b) || b[i] != '"' {
		return "", 0, false
	}
	for j := i + 1; j < len(b); j++ {
		if c := b[j]; c == '"' {
			return string(b[i+1 : j]), j + 1, true
		} else if c == '\\' || c < 0x20 || c >= 0x80 {
			return "", 0, false
		}
	}
	return "", 0, false
}

// neverShare returns the kind of credential found in what the call sends, or
// "". It looks at the text as written, with invisible characters removed, and
// after each layer of percent-decoding, so a key in the userinfo, path, query
// or fragment is found whether or not it is encoded.
func neverShare(args []string) string {
	for _, text := range args {
		text = scan.StripInvisible(text)
		for range maxUnescape + 1 {
			if kind := scan.Find(text); kind != "" {
				return kind
			}
			if !escaped(text) {
				break
			}
			next, err := url.QueryUnescape(text)
			if err != nil {
				// A bad escape elsewhere must not hide a key: decode what decodes.
				next = lenientUnescape(text)
			}
			if next == text {
				break
			}
			text = next
		}
	}
	return ""
}

// escaped reports whether text has a percent escape or a plus, the only things
// percent-decoding changes.
func escaped(text string) bool {
	return strings.IndexByte(text, '%') >= 0 || strings.IndexByte(text, '+') >= 0
}

// lenientUnescape decodes each %XX it can and leaves the rest.
func lenientUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && unhex(s[i+1]) >= 0 && unhex(s[i+2]) >= 0 {
			b.WriteByte(byte(unhex(s[i+1])<<4 | unhex(s[i+2])))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
