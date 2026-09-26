package server

import (
	"net/http/httptest"
	"testing"

	"github.com/erlidev/eika/internal/config"
)

func TestPreviewHost(t *testing.T) {
	const id = "abcdefghijklmnopqrst"
	cases := map[string]struct {
		id   string
		port int
		ok   bool
	}{
		"5173-" + id + ".localhost:8080":      {id, 5173, true},
		"80-" + id + ".eika.example.com":      {id, 80, true},
		"5173-ABCDEFGHIJKLMNOPQRST.localhost": {id, 5173, true},
		"localhost:8080":                      {},
		"127.0.0.1:8080":                      {},
		"5173-" + id:                          {},
		"70000-" + id + ".localhost":          {},
		"5173-" + id[:19] + ".localhost":      {},
		"5173-abcdefghijklmnopqrs1.localhost": {},
		"eika.example.com":                    {},
	}
	for host, want := range cases {
		id, port, ok := previewHost(host)
		if ok != want.ok || id != want.id || port != want.port {
			t.Errorf("previewHost(%q) = %q, %d, %v; want %q, %d, %v", host, id, port, ok, want.id, want.port, want.ok)
		}
	}
}

func TestPreviewBase(t *testing.T) {
	cases := []struct {
		name, publicURL, host, scheme, base string
	}{
		{"a name keeps its port", "", "localhost:8080", "http", "localhost:8080"},
		{"an address becomes localhost", "", "127.0.0.1:8080", "http", "localhost:8080"},
		{"so does an IPv6 one", "", "[::1]:8080", "http", "localhost:8080"},
		{"a name without a port", "", "eika.lan", "http", "eika.lan"},
		{"the public address wins", "https://eika.example.com", "127.0.0.1:8080", "https", "eika.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.PublicURL = c.publicURL
			s := &Server{cfg: cfg}
			r := httptest.NewRequest("POST", "/api/workspaces/x/ports/1/preview", nil)
			r.Host = c.host
			scheme, base := s.previewBase(r)
			if scheme != c.scheme || base != c.base {
				t.Errorf("previewBase = %s, %s; want %s, %s", scheme, base, c.scheme, c.base)
			}
		})
	}
}
