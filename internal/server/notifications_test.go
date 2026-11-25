package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

type notificationTransport func(*http.Request) (*http.Response, error)

func (f notificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNotificationRetriesOnlyRejectedRequests(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status, attempts int
	}{
		{"rate limit", http.StatusTooManyRequests, 2},
		{"proxy timeout", http.StatusGatewayTimeout, 1},
		{"bad gateway", http.StatusBadGateway, 1},
		{"server error", http.StatusServiceUnavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			n := &notifications{server: &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}, client: &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
				count++
				status := tc.status
				if count > 1 {
					status = http.StatusOK
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}}
			n.post(context.Background(), "https://ntfy.example/topic", "Eika replied")
			if count != tc.attempts {
				t.Fatalf("sent %d requests, want %d", count, tc.attempts)
			}
		})
	}
}
