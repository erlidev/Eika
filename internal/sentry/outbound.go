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
	var args struct {
		URL   string `json:"url"`
		Query string `json:"query"`
	}
	switch a.Name {
	case "web_fetch":
		_ = json.Unmarshal(a.Args, &args)
		return []string{args.URL}
	case "web_search":
		_ = json.Unmarshal(a.Args, &args)
		return []string{args.Query}
	}
	return nil
}

// neverShare returns the kind of credential found in what the call sends, or
// "". It looks at the text as written, with invisible characters removed, and
// after each layer of percent-decoding, so a key in the userinfo, path, query
// or fragment is found whether or not it is encoded.
func neverShare(a Action) string {
	for _, text := range outbound(a) {
		text = scan.StripInvisible(text)
		for range maxUnescape + 1 {
			if kind := scan.Find(text); kind != "" {
				return kind
			}
			next, err := url.QueryUnescape(text)
			if err != nil || next == text {
				// A bad escape elsewhere must not hide a key: decode what decodes.
				next = lenientUnescape(text)
				if next == text {
					break
				}
			}
			text = next
		}
	}
	return ""
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
