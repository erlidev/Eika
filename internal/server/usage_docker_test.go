//go:build docker

package server_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

func TestARecordedCallKeepsWhatTheEndpointDidNotStateNull(t *testing.T) {
	a := newAPI(t)
	chat := a.newChat(t, "usage")
	cold := providertest.Usage(90, 100, 190)
	cold.CachedTokens = provider.Reported(0)
	cold.PrefilledTokens = provider.Reported(90)
	warm := providertest.Usage(90, 100, 190)
	warm.CachedTokens = provider.Reported(86)
	a.script(
		providertest.Stream(provider.TextDelta("one"), provider.Event{Kind: provider.KindUsage, Usage: cold}, provider.Done("stop")),
		providertest.Stream(provider.TextDelta("two"), provider.Event{Kind: provider.KindUsage, Usage: warm}, provider.Done("stop")),
		providertest.Text("three"),
	)
	for _, text := range []string{"a", "b", "c"} {
		a.postMessage(t, chat.ID, text, "", 202)
		a.waitIdle(t, chat.ID)
	}

	records := decodeBody[callsWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/calls", nil), 200)
	if len(records.Calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(records.Calls))
	}
	count := func(p *int) string {
		if p == nil {
			return "null"
		}
		return strconv.Itoa(*p)
	}
	var got []string
	for _, r := range records.Calls {
		got = append(got, strings.Join([]string{count(r.PromptTokens), count(r.CompletionTokens), count(r.TotalTokens), count(r.CachedTokens), count(r.PrefilledTokens)}, "/"))
	}
	want := []string{"90/100/190/0/90", "90/100/190/86/4", "null/null/null/null/null"}
	if !slices.Equal(got, want) {
		t.Errorf("prompt/completion/total/cached/prefilled of the three calls = %v, want %v: a stated 0 stays 0, prefilled is the prompt less the cached part where the endpoint did not state it, and a count never stated is null", got, want)
	}

	// The call that stated nothing is not a calibration: the last call that
	// did is.
	preview := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/context", nil), 200)
	if preview.Calibration == nil || preview.Calibration.CallID != records.Calls[1].ID || preview.Calibration.PromptTokens != 90 {
		t.Errorf("calibration = %+v, want the second call's 90 measured input tokens", preview.Calibration)
	}
}
