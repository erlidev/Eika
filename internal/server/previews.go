package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/workspace"
)

// A preview is how the person using a workspace reaches a port of it, such
// as a dev server the agent started, from their browser. The harness is the
// only thing that reaches a sandbox, so it forwards the requests; the
// preview is served on a host of its own, <port>-<workspace id>.<the
// harness's host>, so the page is another origin than the UI and cannot read
// the UI's token. A browser gets in with a one-time ticket the API hands the
// signed-in UI, which the preview trades for a cookie on its own host.
const (
	// previewPath is where a preview host redeems a ticket.
	previewPath = "/__eika/preview"
	// previewCookie holds a preview session. It is never forwarded to the
	// sandbox.
	previewCookie = "eika_preview"
	// ticketLifetime bounds how long a ticket waits to be redeemed.
	ticketLifetime = 2 * time.Minute
	// previewLifetime bounds a preview session.
	previewLifetime = 12 * time.Hour
)

// previewHostPattern matches the first label of a preview's host: the port,
// then the workspace id, which is 20 lowercase base32 characters.
var previewHostPattern = regexp.MustCompile(`^([0-9]{1,5})-([a-z2-7]{20})$`)

// previewResponse is the body of POST /api/workspaces/{id}/ports/{port}/preview.
type previewResponse struct {
	// URL opens the preview: it carries a ticket that works once, within
	// two minutes.
	URL string `json:"url"`
}

// previews holds the preview tickets not yet redeemed and the sessions that
// were.
type previews struct {
	// mu guards everything below it.
	mu       sync.Mutex
	tickets  map[string]previewGrant
	sessions map[string]previewGrant
}

// previewGrant is what a ticket or a session opens: one port of one
// workspace, until it expires.
type previewGrant struct {
	workspaceID string
	port        int
	expires     time.Time
}

// handleOpenPreview hands the UI a URL that opens a preview of one of a
// running workspace's forwarded ports.
func (s *Server) handleOpenPreview(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		s.fail(w, r, invalidf("port %q is not a number", r.PathValue("port")))
		return
	}
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !sandboxPort(ws.Sandbox, port) {
		s.fail(w, r, notFoundf("workspace %s does not forward port %d; add it in the Sandbox panel", ws.ID, port))
		return
	}
	if ws.State != string(workspace.StateRunning) {
		s.fail(w, r, conflictf("workspace %s is %s, not running", ws.ID, ws.State))
		return
	}
	ticket := s.previews.grant(&s.previews.tickets, previewGrant{
		workspaceID: ws.ID, port: port, expires: time.Now().Add(ticketLifetime),
	})
	scheme, host := s.previewBase(r)
	u := url.URL{
		Scheme:   scheme,
		Host:     strconv.Itoa(port) + "-" + ws.ID + "." + host,
		Path:     previewPath,
		RawQuery: url.Values{"ticket": {ticket}}.Encode(),
	}
	writeJSON(w, s.log, http.StatusOK, previewResponse{URL: u.String()})
}

// servePreview serves one request on a preview's host: a ticket's
// redemption, or a request forwarded to the workspace.
func (s *Server) servePreview(w http.ResponseWriter, r *http.Request, id string, port int) {
	if r.URL.Path == previewPath {
		grant, ok := s.previews.take(&s.previews.tickets, r.URL.Query().Get("ticket"), true)
		if !ok || grant.workspaceID != id || grant.port != port {
			previewError(w, http.StatusForbidden, "This preview link has expired or was used already. Open the preview again from the workspace's Sandbox panel in Eika.")
			return
		}
		session := s.previews.grant(&s.previews.sessions, previewGrant{
			workspaceID: id, port: port, expires: time.Now().Add(previewLifetime),
		})
		http.SetCookie(w, &http.Cookie{
			Name:     previewCookie,
			Value:    session,
			Path:     "/",
			MaxAge:   int(previewLifetime.Seconds()),
			HttpOnly: true,
			Secure:   r.TLS != nil || strings.HasPrefix(s.cfg.PublicURL, "https://"),
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	cookie, err := r.Cookie(previewCookie)
	if err != nil {
		previewError(w, http.StatusUnauthorized, "Open this preview from the workspace's Sandbox panel in Eika.")
		return
	}
	grant, ok := s.previews.take(&s.previews.sessions, cookie.Value, false)
	if !ok || grant.workspaceID != id || grant.port != port {
		previewError(w, http.StatusUnauthorized, "This preview's session has ended. Open it again from the workspace's Sandbox panel in Eika.")
		return
	}
	// The workspace is read on every request, so a port taken off the list
	// or a workspace that stopped is closed at once.
	ws, err := s.deps.Store.Workspace(r.Context(), id)
	if err != nil {
		previewError(w, http.StatusNotFound, "The workspace is gone.")
		return
	}
	if !sandboxPort(ws.Sandbox, port) {
		previewError(w, http.StatusForbidden, "The workspace no longer forwards port "+strconv.Itoa(port)+".")
		return
	}
	if ws.State != string(workspace.StateRunning) {
		previewError(w, http.StatusServiceUnavailable, "The workspace is "+ws.State+". Start it in Eika to see the preview.")
		return
	}
	base, err := s.deps.Workspaces.PortURL(r.Context(), workspace.Workspace{ID: ws.ID, ContainerID: ws.ContainerID}, port)
	if err != nil {
		s.log.Error("find preview port", "workspace_id", id, "port", port, "error", err)
		previewError(w, http.StatusBadGateway, "The harness could not find the workspace on its network.")
		return
	}
	target, err := url.Parse(base)
	if err != nil {
		previewError(w, http.StatusBadGateway, "The harness could not find the workspace on its network.")
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// The page's own host, which is what a dev server that checks
			// its Host header expects to see: *.localhost, or the
			// deployment's name.
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			withoutPreviewCookie(pr.Out)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			previewError(w, http.StatusBadGateway, "Nothing answered on port "+strconv.Itoa(port)+
				" of the workspace. A server there must listen on 0.0.0.0, not on localhost, for the harness to reach it.")
		},
	}
	proxy.ServeHTTP(w, r)
}

// previewBase is the scheme and host a preview's host is built on: the
// deployment's public address when it has one, else the host the UI reached
// the API on. An IP address cannot have a subdomain, so a harness reached by
// its address serves previews on localhost, which every browser resolves to
// the same machine with subdomains included.
func (s *Server) previewBase(r *http.Request) (string, string) {
	if s.cfg.PublicURL != "" {
		if u, err := url.Parse(s.cfg.PublicURL); err == nil && u.Host != "" {
			return u.Scheme, u.Host
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, ""
	}
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		host = "localhost"
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme, host
}

// previewHost reads the workspace and port from a preview's host.
func previewHost(host string) (string, int, bool) {
	label, _, ok := strings.Cut(strings.ToLower(host), ".")
	if !ok {
		return "", 0, false
	}
	m := previewHostPattern.FindStringSubmatch(label)
	if m == nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(m[1])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return m[2], port, true
}

// grant stores a grant under a fresh secret and returns the secret. Expired
// grants are dropped on the way.
func (p *previews) grant(table *map[string]previewGrant, g previewGrant) string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	secret := hex.EncodeToString(b[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	if *table == nil {
		*table = map[string]previewGrant{}
	}
	now := time.Now()
	for k, v := range *table {
		if now.After(v.expires) {
			delete(*table, k)
		}
	}
	(*table)[secret] = g
	return secret
}

// take looks a secret up, removing it when once is set, and reports whether
// it names a grant that has not expired.
func (p *previews) take(table *map[string]previewGrant, secret string, once bool) (previewGrant, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := (*table)[secret]
	if !ok {
		return previewGrant{}, false
	}
	if once {
		delete(*table, secret)
	}
	return g, time.Now().Before(g.expires)
}

// withoutPreviewCookie removes the preview's own cookie from a request
// forwarded to the sandbox, keeping the page's cookies.
func withoutPreviewCookie(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != previewCookie {
			r.AddCookie(c)
		}
	}
}

// previewError answers a preview request the harness could not forward, in
// words the person looking at the page can act on.
func previewError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	io.WriteString(w, "Eika preview: "+msg+"\n")
}
