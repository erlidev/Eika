package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// eventsPath is the one route that accepts the token as a query parameter: a
// browser cannot set a header on a WebSocket handshake.
const eventsPath = "/api/events"

// authenticated wraps h so that only requests carrying the configured bearer
// token reach it. The health checks and the git hub are mounted outside it:
// the health checks are public and the hub authenticates workspaces itself,
// with per-workspace credentials.
func (s *Server) authenticated(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="eika"`)
			writeJSON(w, s.log, http.StatusUnauthorized, errorBody{Error: errorDetail{
				Code:    codeUnauthorized,
				Message: "a valid bearer token is required",
			}})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// authorized reports whether the request carries the harness token. The
// comparison is constant time, so a wrong token tells an attacker nothing
// about how much of it was right.
func (s *Server) authorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok && r.URL.Path == eventsPath {
		token, ok = r.URL.Query().Get("token"), true
	}
	if !ok || s.cfg.AuthToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.cfg.AuthToken)) == 1
}
