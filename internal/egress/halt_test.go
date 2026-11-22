package egress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHaltClosesAndRefusesConnections(t *testing.T) {
	var halted atomic.Bool
	p := New(Options{Halted: halted.Load}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer p.Close()
	a, b := net.Pipe()
	defer b.Close()
	if !p.track(a) {
		t.Fatal("connection refused before halt")
	}
	generation := p.currentGeneration()
	halted.Store(true)
	p.Halt()
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("connection after halt: %v", err)
	}
	c, d := net.Pipe()
	defer c.Close()
	defer d.Close()
	if p.track(c) {
		t.Fatal("connection accepted during halt")
	}
	halted.Store(false)
	if p.trackGeneration(generation, c) {
		t.Fatal("connection begun before halt accepted after resume")
	}
	if !p.track(c) {
		t.Fatal("connection refused after resume")
	}
}

func TestProxySendsRefusalsToDecide(t *testing.T) {
	var host, reason string
	p := New(Options{
		Policy: func(context.Context, string) (Policy, error) { return Policy{Token: "token", Mode: ModeAllowlist}, nil },
		Decide: func(_ context.Context, _, h, _, why string) (bool, error) { host, reason = h, why; return false, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer p.Close()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.SetBasicAuth("cx", "token")
	req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || host != "example.com" || reason == "" || len(p.Blocked("cx")) != 1 {
		t.Fatalf("response %d, host %q, reason %q", rec.Code, host, reason)
	}
}

func TestHaltAndResumeDuringDecisionRefusesConnection(t *testing.T) {
	for _, method := range []string{http.MethodConnect, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			var p *Proxy
			var denied bool
			var halted atomic.Bool
			p = New(Options{
				Halted: halted.Load,
				Policy: func(context.Context, string) (Policy, error) { return Policy{Token: "token", Mode: ModePublic}, nil },
				Decide: func(_ context.Context, _, _, _, reason string) (bool, error) {
					if reason != "" {
						denied = true
						return false, nil
					}
					halted.Store(true)
					p.Halt()
					halted.Store(false)
					return true, nil
				},
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer p.Close()
			req := httptest.NewRequest(method, "http://example.com:80/", nil)
			req.SetBasicAuth("cx", "token")
			req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || !denied || len(p.Blocked("cx")) != 1 {
				t.Fatalf("status %d, audited %v, blocked %v", rec.Code, denied, p.Blocked("cx"))
			}
		})
	}
}
