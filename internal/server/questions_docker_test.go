//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

func TestSubagentQuestionReachesHomeAndAnswerContinuesTheCallWithOneSlot(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}),
		providertest.Text("on it"),
		call("answer", "subagent_message", map[string]any{"subagent": "flights", "text": "Tuesday"}), providertest.Text("answered"),
	)
	notices := answeringNotices(eika)
	researcher := newHeldScript(
		call("ask", "ask_user", map[string]any{"question": "Which day?", "options": []string{"Tuesday", "Friday"}}),
		call("report", "report", map[string]any{"summary": "Tuesday is cheapest"}),
		providertest.Text("reported"),
	)
	d.use(byAgent{"You are Eika": notices, "You are a researcher": researcher})
	d.postMessage(t, d.home.ID, "find flights", "", 202)
	<-researcher.started
	d.waitIdle(t, d.home.ID)
	decodeBody[providerWire](t, request(t, d.Server, "PATCH", "/api/providers/"+d.testProvider.ID, map[string]any{"slots": 1}), 200)
	researcher.open()
	waitFor(t, "the child question and background home turn", func() bool {
		row, err := d.store.Subagent(t.Context(), d.home.ID, "flights")
		return err == nil && row.State == store.SessionWaiting && len(notices.Requests()) > 0
	})
	d.settled(t)
	row := d.child(t, "flights")
	if !strings.Contains(row.WaitingReason, "Which day?") || !strings.Contains(row.WaitingReason, "Tuesday") {
		t.Fatalf("waiting = %+v", row)
	}
	if len(d.questions.Pending()) != 0 {
		t.Fatal("subagent question is in the HTTP broker")
	}
	// Eika asks the user in its reply, then relays the next answer.
	d.postMessage(t, d.home.ID, "Tuesday", "", 202)
	waitFor(t, "the child report", func() bool {
		row, err := d.store.Subagent(t.Context(), d.home.ID, "flights")
		return err == nil && row.State == store.SessionIdle
	})
	d.settled(t)
	requests := researcher.Requests()
	if len(requests) != 3 || toolResultsByCall(requests[1])["ask"] != "Tuesday" {
		t.Fatalf("child did not continue its tool call: %+v", requests)
	}
	texts, deliveries := d.homeNotices(t)
	questions := 0
	for id, text := range texts {
		if strings.Contains(text, "asks: Which day?") {
			questions++
			if deliveries[id] != 1 {
				t.Fatalf("question delivered %d times", deliveries[id])
			}
		}
	}
	if questions != 1 {
		t.Fatalf("question notices = %d", questions)
	}
}
