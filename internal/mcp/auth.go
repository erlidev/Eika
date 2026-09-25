package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/mcp/oauth"
)

// authFlowTTL is how long an authorization waits for the browser to come
// back before it is forgotten.
const authFlowTTL = 10 * time.Minute

// refreshEarly is how long before its expiry an access token is refreshed,
// so a request never leaves with one that dies on the way.
const refreshEarly = time.Minute

// ClientMetadataPath is where the harness serves its Client ID Metadata
// Document, below the deployment's public URL.
const ClientMetadataPath = "/oauth/client-metadata.json"

// CallbackPath is the frontend route the authorization server sends the
// browser back to.
const CallbackPath = "/mcp/callback"

// ErrNoAuthorization reports a callback for an authorization the harness is
// not waiting for: finished, expired, or never started here.
var ErrNoAuthorization = errors.New("no authorization is waiting for this answer")

// cachedToken is the access token a server's requests carry.
type cachedToken struct {
	access  string
	expires time.Time
	refresh bool
}

// authFlow is an authorization waiting for the browser to come back with a
// code.
type authFlow struct {
	serverID    string
	verifier    string
	redirectURI string
	scope       string
	discovery   oauth.Discovery
	client      oauth.Client
	created     time.Time
}

// AuthStatus is what the UI shows of a server's authorization.
type AuthStatus struct {
	// Challenged reports that the server asked for authorization.
	Challenged bool
	Challenge  oauth.Challenge
	// Authorized reports that the harness holds an access token for it.
	Authorized   bool
	RefreshToken bool
	Issuer       string
	Resource     string
	// ResourceMetadataURL is where the server's protected resource
	// metadata was found.
	ResourceMetadataURL string
	Scope               string
	ExpiresAt           time.Time
	ClientID            string
	// Registration is how the client came by its id: preregistered,
	// metadata_document, or dynamic.
	Registration string
	UpdatedAt    time.Time
}

// headers returns the headers every request to an HTTP server carries: the
// configured ones, and the access token unless the user configured an
// Authorization header of their own.
func (p *Pool) headers(s *server, cfg ServerConfig) HeaderFunc {
	return func(ctx context.Context) (http.Header, error) {
		h := http.Header{}
		for name, value := range cfg.Headers {
			h.Set(name, value)
		}
		if h.Get("Authorization") != "" {
			return h, nil
		}
		token, err := p.accessToken(ctx, s)
		if err != nil {
			return nil, err
		}
		if token != "" {
			h.Set("Authorization", "Bearer "+token)
		}
		return h, nil
	}
}

// accessToken returns the server's access token, refreshing it first when
// it is about to expire.
func (p *Pool) accessToken(ctx context.Context, s *server) (string, error) {
	s.mu.Lock()
	t := s.token
	s.mu.Unlock()
	if t == nil {
		creds, ok, err := p.opts.Store.MCPCredentials(ctx, s.id)
		if err != nil {
			return "", err
		}
		t = &cachedToken{}
		if ok {
			t = &cachedToken{access: creds.AccessToken, expires: creds.ExpiresAt, refresh: creds.RefreshToken != ""}
		}
		s.mu.Lock()
		s.token = t
		s.mu.Unlock()
	}
	if t.access == "" || t.expires.IsZero() || time.Until(t.expires) > refreshEarly || !t.refresh {
		return t.access, nil
	}
	if err := p.refresh(ctx, s, t.access); err != nil {
		// The token may still be good for a moment; the server says if not.
		s.addLog("eika", "warning", "could not refresh the access token: "+err.Error())
		return t.access, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token.access, nil
}

// refresh trades the server's refresh token for a new access token, unless
// another request already replaced the token that failed.
func (p *Pool) refresh(ctx context.Context, s *server, failed string) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	creds, ok, err := p.opts.Store.MCPCredentials(ctx, s.id)
	if err != nil {
		return err
	}
	if !ok || creds.RefreshToken == "" {
		return errors.New("there is no refresh token")
	}
	if creds.AccessToken != failed && creds.AccessToken != "" {
		p.cacheToken(s, creds)
		return nil
	}
	token, err := oauth.Refresh(ctx, p.opts.HTTPClient, creds.Server, creds.Client, creds.RefreshToken, creds.Resource)
	if err != nil {
		var oe *oauth.Error
		if errors.As(err, &oe) && oe.Code == "invalid_grant" {
			// The grant is gone; only authorizing again brings it back.
			creds.AccessToken, creds.RefreshToken, creds.ExpiresAt = "", "", time.Time{}
			if saveErr := p.opts.Store.SaveMCPCredentials(ctx, s.id, creds); saveErr != nil {
				return saveErr
			}
			p.cacheToken(s, creds)
		}
		return err
	}
	creds.AccessToken, creds.RefreshToken, creds.ExpiresAt = token.AccessToken, token.RefreshToken, token.ExpiresAt
	if token.Scope != "" {
		creds.Scope = token.Scope
	}
	if err := p.opts.Store.SaveMCPCredentials(ctx, s.id, creds); err != nil {
		return err
	}
	p.cacheToken(s, creds)
	s.addLog("eika", "info", "access token refreshed")
	return nil
}

// refreshAfter tries a refresh after the server refused a token with 401,
// and reports whether it is worth trying again.
func (p *Pool) refreshAfter(ctx context.Context, s *server, auth *AuthError) bool {
	if auth.Status != http.StatusUnauthorized {
		return false
	}
	s.mu.Lock()
	t := s.token
	s.mu.Unlock()
	if t == nil || t.access == "" || !t.refresh {
		return false
	}
	return p.refresh(ctx, s, t.access) == nil
}

// cacheToken keeps a server's tokens for its next requests.
func (p *Pool) cacheToken(s *server, creds Credentials) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = &cachedToken{access: creds.AccessToken, expires: creds.ExpiresAt, refresh: creds.RefreshToken != ""}
}

// authStatus reports a server's authorization.
func (p *Pool) authStatus(ctx context.Context, s *server) (AuthStatus, error) {
	creds, ok, err := p.opts.Store.MCPCredentials(ctx, s.id)
	if err != nil {
		return AuthStatus{}, err
	}
	var out AuthStatus
	s.mu.Lock()
	if s.challenge != nil {
		out.Challenged, out.Challenge = true, *s.challenge
	}
	s.mu.Unlock()
	if ok {
		out.Authorized = creds.AccessToken != ""
		out.RefreshToken = creds.RefreshToken != ""
		out.Issuer, out.Resource, out.ResourceMetadataURL = creds.Issuer, creds.Resource, creds.ResourceMetadataURL
		out.Scope, out.ExpiresAt, out.UpdatedAt = creds.Scope, creds.ExpiresAt, creds.UpdatedAt
		out.ClientID, out.Registration = creds.Client.ID, creds.Client.Registration
	}
	return out, nil
}

// BeginAuthorization starts authorizing a server: it discovers the
// server's authorization server, finds or registers a client there, and
// returns the URL to send the browser to. The browser comes back to
// redirectURI, and the frontend hands what it brought to
// CompleteAuthorization.
func (p *Pool) BeginAuthorization(ctx context.Context, id, redirectURI string) (string, error) {
	s, cfg, err := p.lookup(ctx, id)
	if err != nil {
		return "", err
	}
	if cfg.Kind != KindHTTP {
		return "", errors.New("only a remote server is authorized with OAuth; a stdio server takes its credentials from its environment")
	}
	s.mu.Lock()
	var challenge oauth.Challenge
	if s.challenge != nil {
		challenge = *s.challenge
	}
	s.mu.Unlock()
	d, err := oauth.Discover(ctx, p.opts.HTTPClient, cfg.URL, challenge)
	if err != nil {
		return "", err
	}
	creds, stored, err := p.opts.Store.MCPCredentials(ctx, id)
	if err != nil {
		return "", err
	}
	client, err := p.clientFor(ctx, cfg, d, creds, stored, redirectURI)
	if err != nil {
		return "", err
	}
	// A client registered here is kept even if the user never comes back,
	// so trying again does not register another.
	if client.Registration == oauth.RegisteredDynamically && (!stored || creds.Client.ID != client.ID) {
		creds.Issuer, creds.Server, creds.Client, creds.RedirectURI = d.Server.Issuer, d.Server, client, redirectURI
		creds.Resource, creds.ResourceMetadataURL = d.Resource, d.ResourceMetadataURL
		if err := p.opts.Store.SaveMCPCredentials(ctx, id, creds); err != nil {
			return "", err
		}
	}
	granted := ""
	if stored && creds.Issuer == d.Server.Issuer {
		granted = creds.Scope
	}
	scope := chooseScope(challenge, d, granted)
	verifier, codeChallenge, err := oauth.PKCE()
	if err != nil {
		return "", err
	}
	state, err := oauth.State()
	if err != nil {
		return "", err
	}
	target, err := oauth.AuthorizationURL(d.Server, oauth.AuthorizationRequest{
		Client:      client,
		RedirectURI: redirectURI,
		State:       state,
		Challenge:   codeChallenge,
		Scope:       scope,
		Resource:    d.Resource,
	})
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	for key, f := range p.flows {
		if time.Since(f.created) > authFlowTTL {
			delete(p.flows, key)
		}
	}
	p.flows[state] = &authFlow{
		serverID:    id,
		verifier:    verifier,
		redirectURI: redirectURI,
		scope:       scope,
		discovery:   d,
		client:      client,
		created:     time.Now(),
	}
	p.mu.Unlock()
	s.addLog("eika", "info", fmt.Sprintf("authorization started with %s (client %s, %s)", d.Server.Issuer, client.ID, client.Registration))
	return target, nil
}

// clientFor picks the OAuth client to authorize with, in the spec's order:
// the one the user registered by hand, the one registered before at the
// same authorization server, a metadata document, then dynamic
// registration.
func (p *Pool) clientFor(ctx context.Context, cfg ServerConfig, d oauth.Discovery, creds Credentials, stored bool, redirectURI string) (oauth.Client, error) {
	issuer := d.Server.Issuer
	if cfg.OAuthClientID != "" {
		if stored && creds.Client.Registration == oauth.RegisteredByHand && creds.Issuer != "" && creds.Issuer != issuer {
			return oauth.Client{}, fmt.Errorf("the server's authorization server changed from %s to %s, so the client id entered for it may no longer be valid; check it in the server's settings", creds.Issuer, issuer)
		}
		c := oauth.Client{ID: cfg.OAuthClientID, Secret: cfg.OAuthClientSecret, AuthMethod: oauth.AuthNone, Registration: oauth.RegisteredByHand}
		if c.Secret != "" {
			c.AuthMethod = oauth.SecretMethod(d.Server)
		}
		return c, nil
	}
	// A client belongs to the authorization server that registered it, and
	// to the redirect it was registered with.
	if stored && creds.Issuer == issuer && creds.Client.ID != "" && creds.Client.Registration == oauth.RegisteredDynamically && creds.RedirectURI == redirectURI {
		return creds.Client, nil
	}
	if doc, ok := p.metadataClient(redirectURI); ok && d.Server.ClientIDMetadataDocumentSupported {
		return doc, nil
	}
	if d.Server.RegistrationEndpoint != "" {
		return oauth.Register(ctx, p.opts.HTTPClient, d.Server, oauth.Registration{
			ClientName:  p.clientName(),
			ClientURI:   p.opts.PublicURL,
			RedirectURI: redirectURI,
		})
	}
	return oauth.Client{}, fmt.Errorf("the authorization server %s offers no way for Eika to register itself; register a client there by hand, with redirect URI %s, and enter its id in the server's settings", issuer, redirectURI)
}

// metadataClient is the client whose id is the harness's metadata document,
// when the deployment has an https public URL and the browser comes back to
// it.
func (p *Pool) metadataClient(redirectURI string) (oauth.Client, bool) {
	doc, ok := p.ClientMetadataDocument()
	if !ok {
		return oauth.Client{}, false
	}
	if redirects, _ := doc["redirect_uris"].([]string); !slices.Contains(redirects, redirectURI) {
		return oauth.Client{}, false
	}
	return oauth.Client{ID: doc["client_id"].(string), AuthMethod: oauth.AuthNone, Registration: oauth.RegisteredByDocument}, true
}

// ClientMetadataDocument is the harness's Client ID Metadata Document, which
// exists only when the deployment is reached at an https public URL.
func (p *Pool) ClientMetadataDocument() (map[string]any, bool) {
	base := strings.TrimSuffix(p.opts.PublicURL, "/")
	u, err := url.Parse(base)
	if base == "" || err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, false
	}
	return oauth.MetadataDocument(base+ClientMetadataPath, p.clientName(), base, []string{base + CallbackPath}), true
}

func (p *Pool) clientName() string {
	if p.opts.Client.Title != "" {
		return p.opts.Client.Title
	}
	return p.opts.Client.Name
}

// chooseScope picks the scopes to ask for: the challenge's, else what the
// resource says it supports, together with any granted before, so a step-up
// does not lose them; and offline_access when the authorization server
// offers it, for a refresh token.
func chooseScope(ch oauth.Challenge, d oauth.Discovery, granted string) string {
	var scopes []string
	switch {
	case ch.Scope != "":
		scopes = strings.Fields(ch.Scope)
	case len(d.ResourceMetadata.ScopesSupported) > 0:
		scopes = slices.Clone(d.ResourceMetadata.ScopesSupported)
	}
	for _, s := range strings.Fields(granted) {
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	if len(scopes) > 0 && slices.Contains(d.Server.ScopesSupported, "offline_access") && !slices.Contains(scopes, "offline_access") {
		scopes = append(scopes, "offline_access")
	}
	return strings.Join(scopes, " ")
}

// Callback is what the browser brought back from the authorization server.
type Callback struct {
	State string
	Code  string
	// Iss is the RFC 9207 issuer; IssPresent tells an empty one from none.
	Iss              string
	IssPresent       bool
	Error            string
	ErrorDescription string
}

// CompleteAuthorization finishes an authorization: it checks where the
// answer came from, redeems the code, keeps the tokens, and connects the
// server again. It returns the server's id.
func (p *Pool) CompleteAuthorization(ctx context.Context, cb Callback) (string, error) {
	p.mu.Lock()
	f, ok := p.flows[cb.State]
	delete(p.flows, cb.State)
	p.mu.Unlock()
	if !ok || cb.State == "" || time.Since(f.created) > authFlowTTL {
		return "", ErrNoAuthorization
	}
	meta := f.discovery.Server
	// RFC 9207 first: an answer from the wrong server is not acted on at
	// all, not even to show its error.
	if err := oauth.CheckIssuer(meta, cb.Iss, cb.IssPresent); err != nil {
		return f.serverID, err
	}
	if cb.Error != "" {
		message := cb.Error
		if cb.ErrorDescription != "" {
			message += ": " + cb.ErrorDescription
		}
		return f.serverID, fmt.Errorf("the authorization server refused: %s", message)
	}
	if cb.Code == "" {
		return f.serverID, errors.New("the authorization server sent no code")
	}
	token, err := oauth.Exchange(ctx, p.opts.HTTPClient, meta, f.client, cb.Code, f.verifier, f.redirectURI, f.discovery.Resource)
	if err != nil {
		return f.serverID, fmt.Errorf("redeem the authorization code: %w", err)
	}
	scope := token.Scope
	if scope == "" {
		scope = f.scope
	}
	creds := Credentials{
		Issuer:              meta.Issuer,
		Resource:            f.discovery.Resource,
		ResourceMetadataURL: f.discovery.ResourceMetadataURL,
		Server:              meta,
		Client:              f.client,
		RedirectURI:         f.redirectURI,
		AccessToken:         token.AccessToken,
		RefreshToken:        token.RefreshToken,
		Scope:               scope,
		ExpiresAt:           token.ExpiresAt,
	}
	if err := p.opts.Store.SaveMCPCredentials(ctx, f.serverID, creds); err != nil {
		return f.serverID, err
	}
	s, _, err := p.lookup(ctx, f.serverID)
	if err != nil {
		return f.serverID, err
	}
	p.cacheToken(s, creds)
	s.mu.Lock()
	s.challenge = nil
	s.mu.Unlock()
	s.addLog("eika", "info", "authorized by "+meta.Issuer)
	if err := p.Reconnect(ctx, f.serverID, ""); err != nil {
		s.addLog("eika", "warning", "authorized, but the connection failed: "+err.Error())
	}
	return f.serverID, nil
}

// SignOut forgets a server's tokens, asking its authorization server to
// revoke them first. The registered client is kept, so authorizing again
// needs no new registration.
func (p *Pool) SignOut(ctx context.Context, id string) error {
	s, _, err := p.lookup(ctx, id)
	if err != nil {
		return err
	}
	creds, ok, err := p.opts.Store.MCPCredentials(ctx, id)
	if err != nil || !ok {
		return err
	}
	// The refresh token first: revoking it takes its access tokens with it
	// on most servers.
	if err := oauth.Revoke(ctx, p.opts.HTTPClient, creds.Server, creds.Client, creds.RefreshToken, "refresh_token"); err != nil {
		s.addLog("eika", "warning", "could not revoke the refresh token: "+err.Error())
	}
	if err := oauth.Revoke(ctx, p.opts.HTTPClient, creds.Server, creds.Client, creds.AccessToken, "access_token"); err != nil {
		s.addLog("eika", "warning", "could not revoke the access token: "+err.Error())
	}
	creds.AccessToken, creds.RefreshToken, creds.ExpiresAt, creds.Scope = "", "", time.Time{}, ""
	if err := p.opts.Store.SaveMCPCredentials(ctx, id, creds); err != nil {
		return err
	}
	p.cacheToken(s, creds)
	s.addLog("eika", "info", "signed out")
	for _, c := range s.takeConns("") {
		c.close()
	}
	p.emit(s)
	return nil
}
