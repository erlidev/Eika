package provider_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// closingServer is a server that answers the first connection's first request
// and then closes the connection when the next request arrives on it, without
// answering: the keep-alive connection a server closes while the client still
// holds it in its idle pool. Connections after the first are served normally.
type closingServer struct {
	addr string

	mu     sync.Mutex
	conns  int
	bodies []string
}

func newClosingServer(t *testing.T) *closingServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &closingServer{addr: ln.Addr().String()}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			first := s.conns == 1
			s.mu.Unlock()
			go s.serve(conn, first)
		}
	}()
	return s
}

// serve answers the first request on conn and, if closing, closes the
// connection on reading the second without answering it; a connection that
// does not close keeps answering.
func (s *closingServer) serve(conn net.Conn, closing bool) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for n := 0; ; n++ {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		body, _ := io.ReadAll(req.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(body))
		s.mu.Unlock()
		if closing && n == 1 {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Type: text/plain\r\n\r\nok")
	}
}

func (s *closingServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func post(t *testing.T, client *http.Client, url, body string) error {
	t.Helper()
	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func TestHTTPClientRepeatsARequestWhoseReusedConnectionTheServerClosed(t *testing.T) {
	s := newClosingServer(t)
	client := provider.NewHTTPClient(5 * time.Second)
	url := "http://" + s.addr + "/v1/chat/completions"

	if err := post(t, client, url, `{"turn":1}`); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := post(t, client, url, `{"turn":2}`); err != nil {
		t.Fatalf("second request, sent on the connection the server closed: %v", err)
	}
	if got := s.connections(); got != 2 {
		t.Errorf("server accepted %d connections, want 2: the repeat belongs on a fresh one", got)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if want := []string{`{"turn":1}`, `{"turn":2}`, `{"turn":2}`}; strings.Join(s.bodies, "|") != strings.Join(want, "|") {
		t.Errorf("bodies the server read = %q, want %q: the repeat must carry the whole body", s.bodies, want)
	}
}

// TestHTTPClientRepeatsARequestWhenTheTransportSawTheIdleConnectionClose
// provokes net/http's "server closed idle connection" error: the server closes
// the idle connection after the client has picked it from its pool and before
// the request is written, and the transport has seen the end of the stream by
// the time it writes. A POST is not repeated by net/http, and the error is a
// bare unexported value. The test fails on a Go release that rewords it.
func TestHTTPClientRepeatsARequestWhenTheTransportSawTheIdleConnectionClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var conns []net.Conn
	bodies := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			go func() {
				br := bufio.NewReader(conn)
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						return
					}
					_, _ = io.Copy(io.Discard, req.Body)
					mu.Lock()
					bodies++
					mu.Unlock()
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
				}
			}()
		}
	}()
	closeAll := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	}

	client := provider.NewHTTPClient(5 * time.Second)
	url := "http://" + ln.Addr().String() + "/"
	if err := post(t, client, url, `{"turn":1}`); err != nil {
		t.Fatalf("first request: %v", err)
	}

	// Close the pooled connection once the client holds it, and give the
	// transport's reader time to see the end before the request is written.
	closed := false
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if info.WasIdle && !closed {
			closed = true
			closeAll()
			time.Sleep(100 * time.Millisecond)
		}
	}}
	ctx := httptrace.WithClientTrace(context.Background(), trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"turn":2}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("second request, sent on an idle connection the server closed: %v", err)
	}
	_ = resp.Body.Close()
	if !closed {
		t.Fatal("the second request did not reuse the pooled connection, so the test provoked nothing")
	}
	mu.Lock()
	defer mu.Unlock()
	if bodies != 2 {
		t.Errorf("server answered %d requests, want 2", bodies)
	}
}

func TestHTTPClientDoesNotRepeatARequestOnAFreshConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	accepted := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted++
			mu.Unlock()
			_ = conn.Close()
		}
	}()

	client := provider.NewHTTPClient(5 * time.Second)
	if err := post(t, client, "http://"+ln.Addr().String()+"/", `{}`); err == nil {
		t.Fatal("request to a server that closes every connection succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if accepted != 1 {
		t.Errorf("server accepted %d connections, want 1: only a reused connection is worth repeating on", accepted)
	}
}
