//go:build docker

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestHomeExcludedFromChatsAndCannotBeDeleted(t *testing.T) {
	a := newAPI(t)
	home, err := a.store.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	chat, err := a.store.CreateSession(t.Context(), store.Session{Title: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := a.store.AppendEntry(t.Context(), home.ID, store.Entry{Kind: store.KindUser, Payload: []byte(`{"role":"user","content":"keep"}`), Channel: "web"})
	if err != nil {
		t.Fatal(err)
	}
	list := decodeBody[sessionsWire](t, request(t, a.Server, "GET", "/api/sessions?chats=true", nil), 200)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != chat.ID {
		t.Fatalf("chats = %+v", list.Sessions)
	}
	rec := request(t, a.Server, "DELETE", "/api/sessions/"+home.ID, nil)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "home session cannot be deleted") {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := a.store.Entry(t.Context(), saved.ID); err != nil {
		t.Fatalf("home history lost: %v", err)
	}
	read := decodeBody[struct {
		Session sessionWire `json:"session"`
	}](t, request(t, a.Server, "GET", "/api/home", nil), 200)
	if read.Session.ID != home.ID {
		t.Fatalf("home = %+v", read.Session)
	}
	if rec := request(t, a.Server, "DELETE", "/api/sessions/"+chat.ID, nil); rec.Code != 204 {
		t.Fatalf("delete chat = %d", rec.Code)
	}
}

func TestHomeSessionStartsWithTokenOnlyAndSurvivesRestart(t *testing.T) {
	st := storetest.Open(t)
	if _, err := st.PasswordHash(t.Context()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("password = %v", err)
	}

	start := func() string {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		cfg := testConfig()
		cfg.Listen = addr
		s := server.New(cfg, testLogger(), server.Deps{Store: st}, server.Options{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		defer func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("server run: %v", err)
			}
		}()
		client := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			req, err := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/api/home", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", bearer(testToken))
			resp, err := client.Do(req)
			if err == nil {
				var body struct {
					Session sessionWire `json:"session"`
				}
				decodeErr := json.NewDecoder(resp.Body).Decode(&body)
				closeErr := resp.Body.Close()
				if resp.StatusCode != 200 || decodeErr != nil || closeErr != nil || body.Session.ID == "" {
					t.Fatalf("home response: %d, %v, %v, %+v", resp.StatusCode, decodeErr, closeErr, body)
				}
				return body.Session.ID
			}
			select {
			case err := <-done:
				done <- err
				t.Fatalf("server stopped before serving: %v", err)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("server did not serve home")
		return ""
	}
	first := start()
	if second := start(); second != first {
		t.Fatalf("restart home = %q, want %q", second, first)
	}
}

func TestHomeRunRecordsWebChannelAndContext(t *testing.T) {
	a := newAPI(t)
	home, err := a.store.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	fake := a.script(providertest.Text("reply"))
	a.postMessage(t, home.ID, "hello", "", 202)
	a.waitIdle(t, home.ID)
	entries, err := a.store.SessionPath(t.Context(), home.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if entries[0].Kind != store.KindPreamble || entries[0].Channel != "" {
		t.Fatalf("preamble entry = %+v", entries[0])
	}
	for _, entry := range entries[1:] {
		if entry.Channel != "web" {
			t.Errorf("%s channel = %q", entry.Kind, entry.Channel)
		}
	}
	message, ok, err := session.Message(entries[1])
	if err != nil || !ok || message.Role != provider.RoleUser || !strings.Contains(message.Context, "\nchannel: web\n") {
		t.Fatalf("stored user message = %+v, %v", message, err)
	}
	requests := fake.Requests()
	if len(requests) != 1 || len(requests[0].Messages) != 1 || requests[0].Messages[0].Context != message.Context {
		t.Fatalf("provider requests = %+v", requests)
	}
}
