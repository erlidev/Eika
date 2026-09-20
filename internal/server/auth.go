package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
)

// eventsPath is the event stream. It and the terminal sockets are the routes
// that accept the token as a query parameter: a browser cannot set a header on
// a WebSocket handshake.
const eventsPath = "/api/events"

// terminalPattern matches the path of a workspace's terminal socket, the one
// other route that accepts the token in the query.
var terminalPattern = regexp.MustCompile(`^/api/workspaces/[^/]+/terminal$`)

// sessionLifetime is how long a sign-in lasts before the browser signs in
// again.
const sessionLifetime = 30 * 24 * time.Hour

// authStatusResponse is the body of GET /api/auth/status: what the UI must
// know before it can sign in, and nothing else.
type authStatusResponse struct {
	// PasswordSet is false until setup has chosen the sign-in password.
	PasswordSet bool `json:"password_set"`
}

// passwordRequest is the body of POST /api/auth/setup and /api/auth/login.
type passwordRequest struct {
	Password string `json:"password"`
}

// changePasswordRequest is the body of PUT /api/auth/password.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// signInResponse is a new sign-in: the bearer token the browser sends from
// now on and when it stops working.
type signInResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// authenticated wraps h so that only requests carrying a valid bearer token
// reach it: a sign-in session's, or the configured API token. The health
// checks, the sign-in routes, and the git hub are mounted outside it: the
// health checks and sign-in are public and the hub authenticates workspaces
// itself, with per-workspace credentials.
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

// authorized reports whether the request carries the configured API token or
// the token of a sign-in session that has not expired. The API token is
// compared in constant time, so a wrong token tells an attacker nothing about
// how much of it was right; a session token is looked up by its hash.
func (s *Server) authorized(r *http.Request) bool {
	token := bearerToken(r)
	if token == "" {
		return false
	}
	if s.cfg.AuthToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) == 1 {
		return true
	}
	if s.deps.Store == nil {
		return false
	}
	expires, err := s.deps.Store.AuthSessionExpiry(r.Context(), tokenHash(token))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("read sign-in session", "error", err)
		}
		return false
	}
	return time.Now().Before(expires)
}

// bearerToken returns the token a request presents, empty when it presents
// none. Only the WebSocket routes may carry it in the query.
func bearerToken(r *http.Request) string {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(token)
	}
	if isSocketPath(r.URL.Path) {
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return ""
}

// isSocketPath reports whether a path is one of the WebSocket routes, the
// event stream and a workspace's terminal.
func isSocketPath(path string) bool {
	return path == eventsPath || terminalPattern.MatchString(path)
}

// handleAuthStatus tells the UI whether to offer setup or sign-in.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	set, err := s.passwordSet(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, authStatusResponse{PasswordSet: set})
}

// handleSetup chooses the sign-in password of a harness nobody has set up
// yet, and signs the browser that chose it in. Once a password exists the
// route is a conflict, so setup can be claimed once.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[passwordRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := validatePassword(req.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	// The route needs no token, so a harness already set up refuses before
	// it hashes: otherwise anyone could make it derive a hash per request.
	// Hashing is serialized with sign-in for the same reason.
	set, err := s.passwordSet(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if set {
		s.fail(w, r, conflictf("the harness is already set up; sign in instead"))
		return
	}
	s.loginMu.Lock()
	hash, err := hashPassword(req.Password)
	s.loginMu.Unlock()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Store.CreatePasswordHash(r.Context(), hash); err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("the harness is already set up; sign in instead")
		}
		s.fail(w, r, err)
		return
	}
	s.log.Info("sign-in password set")
	s.signIn(w, r, http.StatusCreated)
}

// handleLogin exchanges the password for a session token. Attempts are
// checked one at a time: with the hash's cost that bounds how fast anyone can
// guess, without a lockout that would also keep the owner out.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[passwordRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stored, err := s.deps.Store.PasswordHash(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, conflictf("the harness is not set up yet"))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.loginMu.Lock()
	ok := len(req.Password) <= maxPasswordLength && checkPassword(stored, req.Password)
	s.loginMu.Unlock()
	if !ok {
		s.log.Warn("sign-in refused")
		writeJSON(w, s.log, http.StatusUnauthorized, errorBody{Error: errorDetail{
			Code:    codeUnauthorized,
			Message: "that password is not the one this harness was set up with",
		}})
		return
	}
	s.signIn(w, r, http.StatusOK)
}

// handleLogout ends the session the request was made with. The API token is
// not a session and stays valid.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.DeleteAuthSession(r.Context(), tokenHash(bearerToken(r))); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword replaces the password after checking the current one,
// ends every session, and signs this browser in again, so that a password
// that leaked stops working everywhere at once.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[changePasswordRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stored, err := s.deps.Store.PasswordHash(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.loginMu.Lock()
	ok := len(req.CurrentPassword) <= maxPasswordLength && checkPassword(stored, req.CurrentPassword)
	s.loginMu.Unlock()
	if !ok {
		s.fail(w, r, invalidf("the current password is wrong"))
		return
	}
	if err := validatePassword(req.NewPassword); err != nil {
		s.fail(w, r, err)
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Store.ReplacePasswordHash(r.Context(), hash); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("sign-in password changed")
	s.signIn(w, r, http.StatusOK)
}

// signIn creates a session and answers with its token.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, status int) {
	token, hash, err := newSessionToken()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	expires := time.Now().Add(sessionLifetime).UTC()
	if err := s.deps.Store.CreateAuthSession(r.Context(), hash, expires); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, status, signInResponse{Token: token, ExpiresAt: expires})
}

// passwordSet reports whether setup has chosen a sign-in password.
func (s *Server) passwordSet(r *http.Request) (bool, error) {
	_, err := s.deps.Store.PasswordHash(r.Context())
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}
