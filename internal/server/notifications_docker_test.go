//go:build docker

package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
)

func TestNotificationsForReportAndBackgroundReply(t *testing.T) {
	messages := make(chan string, 4)
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Click") != "https://eika.example/" {
			t.Errorf("click = %q", r.Header.Get("Click"))
		}
		messages <- string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ntfy.Close()
	d := newDelegating(t)
	d.SetNotificationClientForTest(ntfy.Client(), "https://eika.example")
	topic := ntfy.URL + "/secret-topic"
	for _, method := range []string{"PUT", "GET"} {
		var body any
		if method == "PUT" {
			body = map[string]any{"ntfy_topic": topic}
		}
		rec := request(t, d.Server, method, "/api/settings", body)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), topic) || strings.Contains(rec.Body.String(), "secret-topic") {
			t.Fatalf("settings response leaked topic or failed: %d %s", rec.Code, rec.Body.String())
		}
	}
	row, err := d.store.Setting(t.Context(), "ntfy_topic")
	if err != nil || strings.Contains(string(row.Value), "secret-topic") {
		t.Fatal("topic is not sealed")
	}
	eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}), providertest.Text("on it"))
	researcher := newHeldScript(call("done", "report", map[string]any{"summary": "private report"}), providertest.Text("reported"))
	notices := answeringNotices(eika)
	d.use(byAgent{"You are Eika": notices, "You are a researcher": researcher})
	d.postMessage(t, d.home.ID, "find flights", "", 202)
	<-researcher.started
	d.waitIdle(t, d.home.ID)
	researcher.open()
	waitFor(t, "background turn", func() bool { return len(notices.Requests()) == 1 })
	d.settled(t)
	got := map[string]int{}
	for len(got) < 2 {
		select {
		case message := <-messages:
			got[message]++
		case <-time.After(5 * time.Second):
			t.Fatalf("notifications = %v", got)
		}
	}
	if got["A subagent reported"] != 1 || got["Eika replied"] != 1 {
		t.Fatalf("notifications = %v", got)
	}
	restarted := server.New(testConfig(), testLogger(), d.deps, server.Options{})
	restarted.SetNotificationClientForTest(ntfy.Client(), "https://eika.example")
	defer restarted.Close()
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-messages:
		t.Fatalf("extra notification: %q", msg)
	case <-time.After(200 * time.Millisecond):
	}
}
