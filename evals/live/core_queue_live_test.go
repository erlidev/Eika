//go:build live

package live_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/store"
)

type queueGateKey struct{}

type queueGate struct {
	entered     chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	status      int
}

func newQueueGate() *queueGate {
	return &queueGate{entered: make(chan struct{}), release: make(chan struct{})}
}
func (g *queueGate) open() { g.releaseOnce.Do(func() { close(g.release) }) }

type gatedStream struct {
	body io.ReadCloser
	gate *queueGate
	ctx  context.Context
}

func (s *gatedStream) Read(p []byte) (int, error) {
	select {
	case <-s.gate.release:
		return s.body.Read(p)
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	case <-time.After(5 * time.Second):
		return copy(p, ": waiting\n\n"), nil
	}
}
func (s *gatedStream) Close() error { return s.body.Close() }

func queueProxyRewrite(target *url.URL) func(*httputil.ProxyRequest) {
	return func(req *httputil.ProxyRequest) { req.SetURL(target) }
}

// queueProxy lets the real endpoint answer, but holds its response so Core's
// lease stays held while the test observes and fills the queue.
func queueProxy(raw string, gates map[string]*queueGate) (*httptest.Server, string, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, "", err
	}
	prefix := target.Path
	target.Path = ""
	target.RawPath = ""
	proxy := &httputil.ReverseProxy{Rewrite: queueProxyRewrite(target)}
	proxy.ModifyResponse = func(resp *http.Response) error {
		gate, _ := resp.Request.Context().Value(queueGateKey{}).(*queueGate)
		if gate != nil {
			gate.enterOnce.Do(func() { gate.status = resp.StatusCode; close(gate.entered) })
			resp.Body = &gatedStream{body: resp.Body, gate: gate, ctx: resp.Request.Context()}
			resp.ContentLength = -1
			resp.Header.Del("Content-Length")
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/responses")) {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read request", http.StatusBadRequest)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			for marker, gate := range gates {
				if bytes.Contains(raw, []byte(marker)) {
					r = r.WithContext(context.WithValue(r.Context(), queueGateKey{}, gate))
					break
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	return server, server.URL + prefix, nil
}

func (c *liveCore) queueState() ([]inference.PoolState, error) {
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodGet, "http://"+c.address+"/api/_live/inference", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+coreToken)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inference snapshot: %s", resp.Status)
	}
	var state []inference.PoolState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, err
	}
	return state, nil
}

func waitingIndex(p inference.PoolState, id string, priority inference.Priority) int {
	for i, waiting := range p.Waiting {
		if waiting.SessionID == id && waiting.Priority == priority {
			return i
		}
	}
	return -1
}

func (c *liveCore) queueScenario() (err error) {
	ctx := c.t.Context()
	rows, err := c.store.Providers(ctx)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("expected one provider, got %d", len(rows))
	}
	original := rows[0]
	holderGate, waiterGate := newQueueGate(), newQueueGate()
	proxy, baseURL, err := queueProxy(original.BaseURL, map[string]*queueGate{"queue-holder-419": holderGate, "queue-waiter-638": waiterGate})
	if err != nil {
		return err
	}
	defer func() {
		holderGate.open()
		waiterGate.open()
		c.stop()
		if _, e := c.store.UpdateProvider(ctx, original); e != nil {
			err = fmt.Errorf("restore provider: %w", e)
		}
		slots := original.Slots
		if _, e := c.store.OverrideProvider(ctx, original.ID, store.ProviderOverride{Slots: &slots}); e != nil {
			err = fmt.Errorf("restore slots: %w", e)
		}
		proxy.Close()
		c.start()
	}()
	c.stop()
	changed := original
	changed.BaseURL = baseURL
	if _, err := c.store.UpdateProvider(ctx, changed); err != nil {
		return err
	}
	one := 1
	if _, err := c.store.OverrideProvider(ctx, original.ID, store.ProviderOverride{Slots: &one}); err != nil {
		return err
	}
	c.start()
	var pool inference.PoolState
	if err := c.wait(2*time.Minute, func() bool {
		states, err := c.queueState()
		if err != nil || len(states) != 1 {
			return false
		}
		pool = states[0]
		return pool.Slots == 1 && len(pool.Running) == 0 && len(pool.Waiting) == 0
	}); err != nil {
		return fmt.Errorf("one-slot pool idle: %w", err)
	}
	holder, err := c.store.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "queue-holder", Title: "Queue holder"})
	if err != nil {
		return err
	}
	waiter, err := c.store.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "queue-waiter", Title: "Queue waiter"})
	if err != nil {
		return err
	}
	streamURL := "ws://" + c.address + "/api/events?token=" + url.QueryEscape(coreToken) + "&topics=" + url.QueryEscape("session:"+c.home)
	conn, _, err := websocket.Dial(ctx, streamURL, nil)
	if err != nil {
		return fmt.Errorf("open home events: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := c.post("/api/_live/sessions/"+holder.ID+"/background", map[string]string{"text": "queue-holder-419: Give a brief answer without tools."}); err != nil {
		return err
	}
	select {
	case <-holderGate.entered:
		if holderGate.status != http.StatusOK {
			return fmt.Errorf("holder endpoint status %d", holderGate.status)
		}
	case <-time.After(3 * time.Minute):
		return fmt.Errorf("holder did not reach the real endpoint")
	}
	if err := c.post("/api/_live/sessions/"+waiter.ID+"/background", map[string]string{"text": "queue-waiter-638: Give a brief answer without tools."}); err != nil {
		return err
	}
	if err := c.wait(2*time.Minute, func() bool {
		states, err := c.queueState()
		if err != nil || len(states) != 1 {
			return false
		}
		p := states[0]
		return p.Slots == 1 && len(p.Running) == 1 && p.Running[0].SessionID == holder.ID && p.Running[0].Priority == inference.Background && waitingIndex(p, waiter.ID, inference.Background) >= 0
	}); err != nil {
		return fmt.Errorf("background call did not queue: %w", err)
	}
	if err := c.message("Reply with one short sentence about seed storage. Do not use tools."); err != nil {
		return err
	}
	if err := c.wait(2*time.Minute, func() bool {
		states, err := c.queueState()
		if err != nil || len(states) != 1 {
			return false
		}
		p := states[0]
		homeIndex := waitingIndex(p, c.home, inference.Interactive)
		waiterIndex := waitingIndex(p, waiter.ID, inference.Background)
		return len(p.Running) == 1 && p.Running[0].SessionID == holder.ID && homeIndex >= 0 && waiterIndex > homeIndex
	}); err != nil {
		return fmt.Errorf("interactive call did not queue ahead of background: %w", err)
	}
	holderGate.open()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var answerAt time.Time
	for {
		_, raw, err := conn.Read(readCtx)
		if err != nil {
			return fmt.Errorf("read home events: %w", err)
		}
		var e event.Event
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		if e.Type == event.TypeMessageReset {
			return fmt.Errorf("interactive answer was reset")
		}
		if e.Type == event.TypeMessageDelta {
			answerAt = e.Time
		}
		if e.Type == event.TypeTurnEnd {
			break
		}
		if e.Type == event.TypeRunError {
			return fmt.Errorf("interactive run failed")
		}
	}
	if answerAt.IsZero() {
		return fmt.Errorf("interactive run had no answer")
	}
	select {
	case <-waiterGate.entered:
		if waiterGate.status != http.StatusOK {
			return fmt.Errorf("waiting endpoint status %d", waiterGate.status)
		}
	case <-readCtx.Done():
		return fmt.Errorf("waiting background did not start: %w", readCtx.Err())
	}
	states, err := c.queueState()
	if err != nil {
		return err
	}
	var backgroundAt time.Time
	for _, p := range states {
		for _, running := range p.Running {
			if running.SessionID == waiter.ID && running.Priority == inference.Background {
				backgroundAt = running.Started
			}
		}
	}
	if backgroundAt.IsZero() || !answerAt.Before(backgroundAt) {
		return fmt.Errorf("home answer at %s, background admitted at %s", answerAt, backgroundAt)
	}
	c.queueNotes = []string{fmt.Sprintf("home final answer delta: %s; waiting background admitted: %s", answerAt.Format(time.RFC3339Nano), backgroundAt.Format(time.RFC3339Nano))}
	waiterGate.open()
	return nil
}
