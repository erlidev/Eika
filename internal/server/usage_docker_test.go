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

	records := decodeBody[requestsWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/requests", nil), 200)
	if len(records.Requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(records.Requests))
	}
	count := func(p *int) string {
		if p == nil {
			return "null"
		}
		return strconv.Itoa(*p)
	}
	var got []string
	for _, r := range records.Requests {
		got = append(got, strings.Join([]string{count(r.InputTokens), count(r.OutputTokens), count(r.TotalTokens), count(r.CachedTokens)}, "/"))
	}
	want := []string{"90/100/190/0", "90/100/190/86", "null/null/null/null"}
	if !slices.Equal(got, want) {
		t.Errorf("in/out/total/cached of the three calls = %v, want %v: a stated 0 stays 0 and a count never stated is null", got, want)
	}

	// The call that stated nothing is not a calibration: the last call that
	// did is.
	preview := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/context", nil), 200)
	if preview.Calibration == nil || preview.Calibration.RequestID != records.Requests[1].ID || preview.Calibration.InputTokens != 90 {
		t.Errorf("calibration = %+v, want the second call's 90 measured input tokens", preview.Calibration)
	}
}
