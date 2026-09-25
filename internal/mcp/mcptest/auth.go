package mcptest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Authorization is a fake OAuth 2.1 authorization server: metadata,
// dynamic registration, an authorization endpoint that approves at once,
// a token endpoint that checks PKCE and the resource, and revocation. Set
// its switches before the first request.
type Authorization struct {
	Server *httptest.Server
	// Issuer is the server's issuer identifier, its root URL.
	Issuer string
	// NoPKCE leaves code_challenge_methods_supported out of the metadata.
	NoPKCE bool
	// NoRegistration leaves the registration endpoint out.
	NoRegistration bool
	// MetadataDocuments advertises Client ID Metadata Document support.
	MetadataDocuments bool
	// IssParameter advertises and sends the RFC 9207 iss parameter.
	IssParameter bool
	// TokenTTL is how long an access token lives; zero is an hour.
	TokenTTL time.Duration
	// Scopes are the scopes the server says it supports.
	Scopes []string

	mu      sync.Mutex
	clients map[string]registered
	codes   map[string]grant
	access  map[string]grant
	refresh map[string]grant
	revoked []string
	// TokenRequests are the forms the token endpoint received.
	tokenRequests []url.Values
	registrations []map[string]any
	seq           int
}

type registered struct {
	redirects []string
}

type grant struct {
	client    string
	redirect  string
	challenge string
	resource  string
	scope     string
	expires   time.Time
}

// NewAuthorization starts an authorization server that stops when the test
// ends.
func NewAuthorization(t testing.TB) *Authorization {
	t.Helper()
	a := &Authorization{
		clients: map[string]registered{},
		codes:   map[string]grant{},
		access:  map[string]grant{},
		refresh: map[string]grant{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", a.metadata)
	mux.HandleFunc("POST /register", a.register)
	mux.HandleFunc("GET /authorize", a.authorize)
	mux.HandleFunc("POST /token", a.token)
	mux.HandleFunc("POST /revoke", a.revoke)
	a.Server = httptest.NewServer(mux)
	a.Issuer = a.Server.URL
	t.Cleanup(a.Server.Close)
	return a
}

func (a *Authorization) metadata(w http.ResponseWriter, _ *http.Request) {
	meta := map[string]any{
		"issuer":                   a.Issuer,
		"authorization_endpoint":   a.Issuer + "/authorize",
		"token_endpoint":           a.Issuer + "/token",
		"revocation_endpoint":      a.Issuer + "/revoke",
		"response_types_supported": []string{"code"},
		"grant_types_supported":    []string{"authorization_code", "refresh_token"},
	}
	if !a.NoPKCE {
		meta["code_challenge_methods_supported"] = []string{"S256"}
	}
	if !a.NoRegistration {
		meta["registration_endpoint"] = a.Issuer + "/register"
	}
	if a.MetadataDocuments {
		meta["client_id_metadata_document_supported"] = true
	}
	if a.IssParameter {
		meta["authorization_response_iss_parameter_supported"] = true
	}
	if len(a.Scopes) > 0 {
		meta["scopes_supported"] = a.Scopes
	}
	writeJSON(w, http.StatusOK, meta)
}

func (a *Authorization) register(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	var redirects []string
	if list, ok := body["redirect_uris"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				redirects = append(redirects, s)
			}
		}
	}
	a.mu.Lock()
	a.seq++
	id := fmt.Sprintf("client-%d", a.seq)
	a.clients[id] = registered{redirects: redirects}
	a.registrations = append(a.registrations, body)
	a.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"client_id": id, "token_endpoint_auth_method": "none", "redirect_uris": redirects})
}

func (a *Authorization) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.mu.Lock()
	client, known := a.clients[q.Get("client_id")]
	a.mu.Unlock()
	redirect := q.Get("redirect_uri")
	switch {
	case !known && !strings.HasPrefix(q.Get("client_id"), "https://"):
		http.Error(w, "unknown client", http.StatusBadRequest)
		return
	case known && !slices.Contains(client.redirects, redirect):
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	case q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		http.Error(w, "PKCE S256 required", http.StatusBadRequest)
		return
	case q.Get("resource") == "":
		http.Error(w, "resource required", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	a.seq++
	code := fmt.Sprintf("code-%d", a.seq)
	a.codes[code] = grant{client: q.Get("client_id"), redirect: redirect, challenge: q.Get("code_challenge"), resource: q.Get("resource"), scope: q.Get("scope")}
	a.mu.Unlock()
	back, _ := url.Parse(redirect)
	values := back.Query()
	values.Set("code", code)
	values.Set("state", q.Get("state"))
	if a.IssParameter {
		values.Set("iss", a.Issuer)
	}
	back.RawQuery = values.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (a *Authorization) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	form := r.PostForm
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokenRequests = append(a.tokenRequests, form)
	var g grant
	switch form.Get("grant_type") {
	case "authorization_code":
		var ok bool
		g, ok = a.codes[form.Get("code")]
		delete(a.codes, form.Get("code"))
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		switch {
		case !ok:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "PKCE verification failed"})
			return
		case form.Get("redirect_uri") != g.redirect || form.Get("client_id") != g.client || form.Get("resource") != g.resource:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "the grant does not match"})
			return
		}
	case "refresh_token":
		var ok bool
		g, ok = a.refresh[form.Get("refresh_token")]
		delete(a.refresh, form.Get("refresh_token"))
		if !ok || form.Get("resource") != g.resource {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	ttl := a.TokenTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	a.seq++
	access, refresh := fmt.Sprintf("access-%d", a.seq), fmt.Sprintf("refresh-%d", a.seq)
	g.expires = time.Now().Add(ttl)
	a.access[access] = g
	a.refresh[refresh] = g
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(ttl.Seconds()),
		"refresh_token": refresh,
		"scope":         g.scope,
	})
}

func (a *Authorization) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	a.mu.Lock()
	defer a.mu.Unlock()
	token := r.PostForm.Get("token")
	a.revoked = append(a.revoked, token)
	delete(a.access, token)
	delete(a.refresh, token)
	w.WriteHeader(http.StatusOK)
}

// Valid reports whether an Authorization header carries a live access
// token issued for resource.
func (a *Authorization) Valid(header, resource string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.access[token]
	return ok && time.Now().Before(g.expires) && g.resource == resource
}

// ProtectedResource serves the protected resource metadata of the MCP
// server at resource, naming this authorization server.
func (a *Authorization) ProtectedResource(resource string, scopes ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		body := map[string]any{"resource": resource, "authorization_servers": []string{a.Issuer}}
		if len(scopes) > 0 {
			body["scopes_supported"] = scopes
		}
		writeJSON(w, http.StatusOK, body)
	}
}

// Approve plays the browser: it opens the authorization URL and returns
// where the authorization server sends it back to.
func (a *Authorization) Approve(ctx context.Context, authorizationURL string) (*url.URL, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("authorize answered HTTP %d", resp.StatusCode)
	}
	return url.Parse(resp.Header.Get("Location"))
}

// TokenRequests returns the forms the token endpoint received.
func (a *Authorization) TokenRequests() []url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.tokenRequests)
}

// Registrations returns the bodies of the dynamic registrations received.
func (a *Authorization) Registrations() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.registrations)
}

// Revoked returns the tokens revoked, in order.
func (a *Authorization) Revoked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.revoked)
}

// Expire makes every access token issued so far expire.
func (a *Authorization) Expire() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, g := range a.access {
		g.expires = time.Now().Add(-time.Second)
		a.access[token] = g
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
