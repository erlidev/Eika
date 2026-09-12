package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/server"
)

// testLogger returns a logger that discards everything.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealth(t *testing.T) {
	s := server.New(config.Default(), testLogger(), server.Options{})
	for _, path := range []string{"/healthz", "/api/healthz"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body.Status != "ok" {
				t.Errorf("status = %q, want ok", body.Status)
			}
		})
	}
}

func TestUnknownRouteWithoutWebDir(t *testing.T) {
	s := server.New(config.Default(), testLogger(), server.Options{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebDirServesIndexForClientRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Eika</h1>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatalf("make assets dir: %v", err)
	}
	s := server.New(config.Default(), testLogger(), server.Options{WebDir: dir})

	// "/assets" is a real directory: it must render the app, not a listing.
	for _, path := range []string{"/", "/workspaces/42", "/assets", "/assets/"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, rec.Code)
		}
		if rec.Body.String() != "<h1>Eika</h1>" {
			t.Errorf("%s body = %q, want the index", path, rec.Body.String())
		}
	}
}

func TestServeStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, "127.0.0.1:0", http.NotFoundHandler(), testLogger())
	}()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve() = %v, want nil", err)
	}
}

func TestServeReportsABadAddress(t *testing.T) {
	err := server.Serve(t.Context(), "not-an-address", http.NotFoundHandler(), testLogger())
	if err == nil {
		t.Fatal("Serve accepted a bad address")
	}
}
