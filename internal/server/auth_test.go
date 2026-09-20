package server_test

import (
	"net/http"
	"testing"

	"github.com/erlidev/eika/internal/server"
)

func TestAuthRejectsEverythingButTheRightToken(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong token", "Bearer nope"},
		{"a prefix of the token", "Bearer test-toke"},
		{"the token with more after it", "Bearer test-tokens"},
		{"another scheme", "Basic " + testToken},
		{"the bare token", testToken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := requestWith(t, s, http.MethodGet, "/api/projects", nil, c.header)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got == "" {
				t.Error("no WWW-Authenticate header on a 401")
			}
			code, _ := errorOf(t, rec)
			if code != "unauthorized" {
				t.Errorf("code = %q, want unauthorized", code)
			}
		})
	}
}

func TestAuthLetsThePublicRoutesThrough(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{Hub: newFakeHub()}, server.Options{})
	cases := []struct {
		name string
		path string
	}{
		{"container health check", "/healthz"},
		{"frontend health check", "/api/healthz"},
		{"the git hub, which authenticates workspaces itself", "/git/demo.git/info/refs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := requestWith(t, s, http.MethodGet, c.path, nil, "")
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthAcceptsTheTokenInTheQueryForTheSocketsOnly(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})

	// The API rejects a query token, so that a token cannot leak through a
	// link or a log of ordinary requests. Paths that only resemble a socket's
	// are ordinary requests.
	for _, path := range []string{
		"/api/projects",
		"/api/workspaces/w1/diff",
		"/api/workspaces/w1/file",
		"/api/workspaces/w1/terminal/extra",
		"/api/workspaces/a/b/terminal",
		"/api/events/extra",
	} {
		rec := requestWith(t, s, http.MethodGet, path+"?token="+testToken, nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want 401", path, rec.Code)
		}
	}

	// The sockets accept it, because a browser cannot set a header on a
	// WebSocket handshake. Without the handshake headers the upgrade fails,
	// or the route does not exist on a harness without a host, which is past
	// authentication and enough for this test.
	for _, path := range []string{"/api/events", "/api/workspaces/w1/terminal"} {
		rec := requestWith(t, s, http.MethodGet, path+"?token="+testToken, nil, "")
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s status = %d, want anything but 401", path, rec.Code)
		}
		rec = requestWith(t, s, http.MethodGet, path+"?token=nope", nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with a wrong token = %d, want 401", path, rec.Code)
		}
	}
}

func TestAuthRejectsEveryRequestWhenNoTokenIsConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.AuthToken = ""
	s := server.New(cfg, testLogger(), server.Deps{}, server.Options{})
	rec := requestWith(t, s, http.MethodGet, "/api/projects", nil, "Bearer ")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
