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

	// A provider the harness has not probed is counted by estimate, and the
	// drift is there where there is a measurement to compare it with and
	// null where there is not: it is never the prediction itself.
	for i, r := range records.Calls {
		if r.Prediction != "estimated" || r.PredictedPromptTokens == nil || *r.PredictedPromptTokens <= 0 {
			t.Errorf("call %d: prediction %q of %v, want an estimate", i+1, r.Prediction, r.PredictedPromptTokens)
		}
		if measured := r.PromptTokens != nil; measured != (r.Drift != nil) {
			t.Errorf("call %d: measured %t but drift %v; drift exists exactly when both numbers do", i+1, measured, r.Drift)
		}
		if r.Drift != nil && *r.Drift != *r.PredictedPromptTokens-*r.PromptTokens {
			t.Errorf("call %d: drift %d, want predicted %d less measured %d", i+1, *r.Drift, *r.PredictedPromptTokens, *r.PromptTokens)
		}
	}

	// A recorded call's breakdown states its remainder against what the
	// endpoint measured, and says null, not 0, for a call that measured nothing.
	for i, want := range []string{"90", "90", "null"} {
		recorded := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/calls/"+records.Calls[i].ID, nil), 200)
		b := recorded.Breakdown
		if b == nil {
			t.Fatalf("call %d has no breakdown", i+1)
		}
		if count(b.PromptTokens) != want || (want == "null") != (b.Remainder == nil) || (want == "null") != (b.PromptSource == "") {
			t.Errorf("call %d: prompt %s, source %q, remainder %v; want %s with a remainder and source exactly when there is a prompt",
				i+1, count(b.PromptTokens), b.PromptSource, b.Remainder, want)
		}
		if b.PromptTokens != nil && (b.PromptSource != "measured" || *b.Remainder != *b.PromptTokens-b.Parts) {
			t.Errorf("call %d: remainder %d, want the measured %d less the parts' %d", i+1, *b.Remainder, *b.PromptTokens, b.Parts)
		}
		if len(b.Messages) != len(recorded.Messages) || len(b.Sections) != len(recorded.Sections) || len(b.Tools) != len(recorded.Tools) {
			t.Errorf("call %d: breakdown has %d sections, %d tools, %d messages for %d, %d, %d parts",
				i+1, len(b.Sections), len(b.Tools), len(b.Messages), len(recorded.Sections), len(recorded.Tools), len(recorded.Messages))
		}
	}

	// The next request is previewed with a prediction, and with no measurement
	// to state a remainder against, since this provider is only estimated.
	preview := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/context", nil), 200)
	if preview.Prediction == nil || preview.Prediction.Method != "estimated" || preview.Prediction.Tokens <= 0 {
		t.Errorf("prediction = %+v, want an estimate", preview.Prediction)
	}
	if b := preview.Breakdown; b == nil || b.PromptTokens != nil || b.Remainder != nil || !b.Approximate() {
		t.Errorf("breakdown = %+v, want approximate parts and no remainder against an estimate", b)
	}
}
