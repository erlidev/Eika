package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// MarkSensitiveRead is an access mark: a file tool's path, or the text of a
// bash command, matched the sensitive list. Its value is the entry that matched.
const MarkSensitiveRead MarkKind = "sensitive-read"

// Limits on the sensitive list setting.
const (
	maxSensitiveEntries = 100
	maxSensitiveEntry   = 200
)

// sensitiveEnd closes a name in a path or a command: a separator, a quote,
// white space or the end of the text.
const sensitiveEnd = `(?:[/\s'"]|$)`

// sensitiveStart opens a name: the start of the text or whatever precedes a
// path component in a command.
const sensitiveStart = `(?:^|[/\s'"=])`

// DefaultSensitiveList is the starting value of the `sensitive_list` setting:
// regular expressions matched against a file tool's path and a bash command.
// They cover SSH and cloud credential files, .env files, browser profiles,
// /proc/*/environ, and a bare env or printenv.
var DefaultSensitiveList = []string{
	sensitiveStart + `\.ssh` + sensitiveEnd,
	sensitiveStart + `\.aws` + sensitiveEnd,
	sensitiveStart + `\.azure` + sensitiveEnd,
	sensitiveStart + `\.gnupg` + sensitiveEnd,
	`\.config/gcloud` + sensitiveEnd,
	`\.kube/config` + sensitiveEnd,
	`\.docker/config\.json` + sensitiveEnd,
	sensitiveStart + `\.(?:netrc|pgpass|npmrc|pypirc)` + sensitiveEnd,
	sensitiveStart + `\.env(?:\.[\w.-]+)?` + sensitiveEnd,
	sensitiveStart + `\.mozilla` + sensitiveEnd,
	`\.config/(?:google-chrome|chromium|BraveSoftware|microsoft-edge)` + sensitiveEnd,
	`Library/Application Support/(?:Google/Chrome|Firefox)` + sensitiveEnd,
	`/proc/(?:self|\d+|\*)/environ`,
	`(?:^\s*|[;&|(]\s*)(?:env|printenv)\s*(?:$|[;&|)])`,
}

// ValidateSensitiveList checks a value of the setting.
func ValidateSensitiveList(entries []string) error {
	if len(entries) > maxSensitiveEntries {
		return fmt.Errorf("at most %d entries", maxSensitiveEntries)
	}
	for _, e := range entries {
		if e == "" || len(e) > maxSensitiveEntry {
			return fmt.Errorf("each entry is 1 to %d characters", maxSensitiveEntry)
		}
		if _, err := regexp.Compile(e); err != nil {
			return fmt.Errorf("entry %q is not a regular expression", e)
		}
	}
	return nil
}

// sensitiveMatcher is the compiled list, kept until the setting changes.
type sensitiveMatcher struct {
	mu      sync.Mutex
	key     string
	entries []string
	res     []*regexp.Regexp
}

func (m *sensitiveMatcher) compile(entries []string) ([]string, []*regexp.Regexp) {
	key := strings.Join(entries, "\x00")
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.res == nil || key != m.key {
		m.key, m.entries, m.res = key, entries, nil
		for _, e := range entries {
			if re, err := regexp.Compile(e); err == nil {
				m.res = append(m.res, re)
			} else {
				m.res = append(m.res, nil) // keeps entries and res aligned
			}
		}
	}
	return m.entries, m.res
}

// sensitiveText is what a call exposes to the list: a file tool's path, a
// bash command's text. Other calls have none.
func sensitiveText(a Action) string {
	if a.Kind != KindTool {
		return ""
	}
	var args struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	switch a.Name {
	case "file_read", "file_write", "file_edit":
		_ = json.Unmarshal(a.Args, &args)
		return args.Path
	case "bash":
		_ = json.Unmarshal(a.Args, &args)
		return args.Command
	}
	return ""
}

// markSensitive records a sensitive-read mark when an allowed call reaches
// something on the list. Like every mark it changes no verdict, and a list or
// mark that cannot be read or written only costs the mark: the call runs.
func (g *gate) markSensitive(ctx context.Context, a Action) {
	if g.Marks == nil {
		return
	}
	text := sensitiveText(a)
	if text == "" {
		return
	}
	entries := DefaultSensitiveList
	if g.SensitiveList != nil {
		got, err := g.SensitiveList(ctx)
		if err != nil {
			g.Log.Error("read sensitive list", "error", err)
			return
		}
		entries = got
	}
	entries, res := g.sensitive.compile(entries)
	for i, re := range res {
		if re == nil || !re.MatchString(text) {
			continue
		}
		m := Mark{Session: a.Principal.Session, Run: a.Principal.Run, Computer: a.Principal.Computer,
			Kind: MarkSensitiveRead, Value: entries[i], SourceName: a.Name, Key: a.Key}
		if err := g.mark(ctx, m); err != nil {
			g.Log.Error("record sensitive-read", "error", err)
		}
		return
	}
}
