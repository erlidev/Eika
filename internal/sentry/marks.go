package sentry

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/sentry/trip"
)

// DefaultWatchThreshold is the score at or over which a window's destinations
// are watched. It is lower than the removal threshold: a window can look
// suspicious without being removed. The setting `watch_threshold` replaces it.
const DefaultWatchThreshold = 0.5

// MarkKind names a mark (docs/design/sentry.md, section 11).
type MarkKind string

// The marks recorded so far. Later packages add the rest.
const (
	// MarkWatched is a source: ingest scored a window at or over the watch
	// threshold, and each host, URL and address in it is watched.
	MarkWatched MarkKind = "watched"
	// MarkHidden is a source: ingest removed tag characters that decode to
	// text. The decoded text counts as a watched window whatever its score.
	MarkHidden MarkKind = "hidden"
)

// Mark is one row of a run's marks. It changes nothing by itself; only a
// chain of marks acts.
type Mark struct {
	ID         string
	At         time.Time
	Session    string
	Run        string
	Computer   string // the computer a tool call ran on, when it has one
	Kind       MarkKind
	Value      string // the watched host; empty for a mark without one
	SourceName string // where the content came from, cleaned like a withheld row
	Key        string // the tool call that brought the content
	Score      float64
}

// MarkLog stores marks. Core supplies it (store.Store implements it).
type MarkLog interface {
	AppendMark(ctx context.Context, m Mark) error
}

// maxWatchedPerWindow bounds the marks one window can write, so a page of
// thousands of names costs a bounded number of rows.
const maxWatchedPerWindow = 50

// minHiddenRun is how many decoded tag characters make a hidden mark. Subdivision
// flag emoji use up to six, so shorter runs are ordinary text.
const minHiddenRun = 8

var (
	urlPattern  = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"'\x60)\]]+`)
	hostPattern = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}\b|\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

// hostsIn returns the distinct canonical hosts, URLs' hosts and addresses in
// text, at most maxWatchedPerWindow, in order of appearance.
func hostsIn(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(host string) {
		host = CanonicalHost(host)
		if host == "" || seen[host] || len(out) >= maxWatchedPerWindow {
			return
		}
		seen[host] = true
		out = append(out, host)
	}
	for _, raw := range urlPattern.FindAllString(text, -1) {
		if u, err := url.Parse(raw); err == nil {
			add(u.Hostname())
		}
	}
	for _, host := range hostPattern.FindAllString(text, -1) {
		add(host)
	}
	return out
}

// hiddenText decodes the runs of Unicode tag characters in text into the ASCII
// they spell. Only runs of minHiddenRun or more count.
func hiddenText(text string) string {
	var out, run strings.Builder
	flush := func() {
		if run.Len() >= minHiddenRun {
			out.WriteString(run.String())
			out.WriteByte('\n')
		}
		run.Reset()
	}
	for _, r := range text {
		if r >= 0xE0020 && r <= 0xE007E {
			run.WriteRune(r - 0xE0000)
			continue
		}
		if r == 0xE007F || r == 0xE0001 {
			continue
		}
		flush()
	}
	flush()
	return out.String()
}

// watchThreshold is the setting's value, or the default.
func (g *gate) watchThreshold(ctx context.Context) (float64, error) {
	if g.WatchThreshold == nil {
		return DefaultWatchThreshold, nil
	}
	t, err := g.WatchThreshold(ctx)
	if err != nil {
		return 0, fmt.Errorf("sentry: read watch threshold: %w", err)
	}
	return t, nil
}

// mark writes one mark. At is set when it is zero.
func (g *gate) mark(ctx context.Context, m Mark) error {
	if m.At.IsZero() {
		m.At = time.Now().UTC()
	}
	g.trip.Add(m.Run, trip.Mark{Kind: trip.Kind(m.Kind), Call: m.Key, Computer: m.Computer})
	if err := g.Marks.AppendMark(ctx, m); err != nil {
		return fmt.Errorf("sentry: record mark: %w", err)
	}
	return nil
}

// sourceMark writes one mark of a segment's source.
func (g *gate) sourceMark(ctx context.Context, src Source, kind MarkKind, value string, score float64) error {
	if kind == MarkWatched {
		g.noteWatched(src.Session, value)
	}
	return g.mark(ctx, Mark{Session: src.Session, Run: src.Run, Kind: kind, Value: value, SourceName: src.Name, Key: src.Key, Score: score})
}
