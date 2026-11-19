package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/store"
)

const ntfySetting = "ntfy_topic"

type notifications struct {
	server *Server
	client *http.Client
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

func newNotifications(s *Server) *notifications {
	ctx, cancel := context.WithCancel(context.Background())
	client := fetch.NewClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &notifications{server: s, client: client, ctx: ctx, cancel: cancel}
}

func (n *notifications) send(message string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.server.deps.Store == nil {
		return
	}
	n.wg.Go(func() { n.deliver(message) })
}

func (n *notifications) close() {
	n.mu.Lock()
	n.closed = true
	n.cancel()
	n.mu.Unlock()
	n.wg.Wait()
}

func (n *notifications) deliver(message string) {
	ctx, cancel := context.WithTimeout(n.ctx, 45*time.Second)
	defer cancel()
	row, err := n.server.deps.Store.Setting(ctx, ntfySetting)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		n.server.log.Warn("read notification configuration")
		return
	}
	var sealed []byte
	if json.Unmarshal(row.Value, &sealed) != nil || len(sealed) == 0 {
		return
	}
	topic, err := n.server.deps.Secrets.Open(sealed)
	if err != nil {
		n.server.log.Warn("open notification configuration")
		return
	}
	n.post(ctx, topic, message)
}

func (n *notifications) post(ctx context.Context, topic, message string) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, topic, strings.NewReader(message))
		if err != nil {
			n.server.log.Warn("create notification request")
			return
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		req.Header.Set("Click", n.server.cfg.PublicURL+"/")
		resp, err := n.client.Do(req)
		if err != nil {
			n.server.log.Warn("send notification: outcome unknown")
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			n.server.log.Warn("send notification", "status", resp.StatusCode)
			return
		}
		n.server.log.Warn("send notification; trying again", "status", resp.StatusCode)
		if attempt == 2 {
			return
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func validTopic(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Fragment == "" && u.RawQuery == "" && u.Path != "" && u.Path != "/"
}

func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	row, err := s.deps.Store.Setting(r.Context(), ntfySetting)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, invalidf("configure an ntfy topic first"))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var sealed []byte
	if json.Unmarshal(row.Value, &sealed) != nil || len(sealed) == 0 {
		s.fail(w, r, invalidf("configure an ntfy topic first"))
		return
	}
	s.notifications.send("Eika test notification")
	w.WriteHeader(http.StatusNoContent)
}
