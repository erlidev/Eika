package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sync"
)

// sseTransport speaks the HTTP+SSE transport of the 2024-11-05 revision: one
// GET stream carries everything the server sends, and the client POSTs its
// messages to the endpoint the stream's first event names. It is deprecated
// and used only for a server that answers nothing else.
type sseTransport struct {
	endpoint string
	client   *http.Client
	headers  HeaderFunc
	recv     receiver

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
}

// dialSSE opens the stream at streamURL and waits, as long as ctx allows, for
// the endpoint it names.
func dialSSE(ctx context.Context, streamURL string, client *http.Client, headers HeaderFunc, recv receiver) (*sseTransport, error) {
	base, err := url.Parse(streamURL)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", streamURL, err)
	}
	life, stop := context.WithCancel(context.Background())
	t := &sseTransport{client: client, headers: headers, recv: recv, ctx: life, stop: stop}
	// The stream outlives the dial, so it runs on the transport's context;
	// the dial only bounds how long the first event may take.
	req, err := http.NewRequestWithContext(life, http.MethodGet, streamURL, nil)
	if err != nil {
		stop()
		return nil, fmt.Errorf("build request: %w", err)
	}
	if err := t.setHeaders(req); err != nil {
		stop()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	type answer struct {
		resp *http.Response
		err  error
	}
	answered := make(chan answer, 1)
	go func() {
		resp, err := client.Do(req)
		answered <- answer{resp, err}
	}()
	var resp *http.Response
	select {
	case a := <-answered:
		if a.err != nil {
			stop()
			return nil, fmt.Errorf("open event stream: %w", a.err)
		}
		resp = a.resp
	case <-ctx.Done():
		// Stopping the transport ends the request, and with it the
		// goroutine, which closes whatever it got.
		stop()
		go func() {
			if a := <-answered; a.resp != nil {
				a.resp.Body.Close()
			}
		}()
		return nil, fmt.Errorf("open event stream: %w", ctx.Err())
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		defer stop()
		return nil, statusError(resp, false)
	}
	if contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); contentType != "text/event-stream" {
		drain(resp.Body)
		stop()
		return nil, fmt.Errorf("the server answered GET with %q, not an event stream", contentType)
	}

	endpoint := make(chan string, 1)
	t.wg.Add(1)
	go t.read(resp, base, endpoint)
	select {
	case e, ok := <-endpoint:
		if !ok {
			t.close()
			return nil, errors.New("the event stream ended before it named an endpoint")
		}
		t.endpoint = e
		return t, nil
	case <-ctx.Done():
		t.close()
		return nil, fmt.Errorf("wait for the endpoint event: %w", ctx.Err())
	}
}

// read delivers the stream's events: the endpoint first, then messages.
func (t *sseTransport) read(resp *http.Response, base *url.URL, endpoint chan<- string) {
	defer t.wg.Done()
	defer resp.Body.Close()
	events := newSSEReader(resp.Body)
	named := false
	for {
		ev, err := events.next()
		if err != nil {
			if !named {
				close(endpoint)
			}
			if errors.Is(err, io.EOF) {
				err = errors.New("the server closed its event stream")
			}
			t.recv.closed(err)
			return
		}
		switch {
		case ev.name == "endpoint" && !named:
			target, err := base.Parse(ev.data)
			// The endpoint must be on the server's own origin, or a server
			// could have the client post its messages, and its token,
			// anywhere.
			if err != nil || target.Scheme != base.Scheme || target.Host != base.Host {
				close(endpoint)
				t.recv.closed(fmt.Errorf("the server named endpoint %q, which is not on its own origin", quote(ev.data)))
				return
			}
			named = true
			endpoint <- target.String()
		case ev.name == "message":
			msgs, err := decodeMessages([]byte(ev.data))
			if err != nil {
				continue
			}
			for _, m := range msgs {
				t.recv.receive(m, "")
			}
		}
	}
}

func (t *sseTransport) setHeaders(req *http.Request) error {
	if t.headers == nil {
		return nil
	}
	extra, err := t.headers(req.Context())
	if err != nil {
		return err
	}
	for name, values := range extra {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	return nil
}

func (t *sseTransport) send(ctx context.Context, m *message, h sendHeaders) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode %s: %w", h.method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if err := t.setHeaders(req); err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", h.method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp, false)
	}
	// The answer arrives on the stream; the POST is only acknowledged.
	drain(resp.Body)
	return nil
}

func (t *sseTransport) cancelled(ctx context.Context, id string) {
	m, err := notification(notifyCancelled, map[string]any{"requestId": json.RawMessage(id), "reason": "the caller gave up"})
	if err != nil {
		return
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	_ = t.send(sendCtx, m, sendHeaders{method: notifyCancelled})
}

func (t *sseTransport) close() error {
	t.stop()
	t.wg.Wait()
	return nil
}
