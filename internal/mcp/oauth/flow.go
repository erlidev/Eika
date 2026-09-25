package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// The ways a client authenticates at the token endpoint.
const (
	AuthNone   = "none"
	AuthBasic  = "client_secret_basic"
	AuthPost   = "client_secret_post"
	grantCode  = "authorization_code"
	grantFresh = "refresh_token"
)

// How a client came by its id.
const (
	// RegisteredByHand is a client id the user entered.
	RegisteredByHand = "preregistered"
	// RegisteredByDocument is a client id that is the URL of a Client ID
	// Metadata Document the harness serves.
	RegisteredByDocument = "metadata_document"
	// RegisteredDynamically is a client id from RFC 7591 registration.
	RegisteredDynamically = "dynamic"
)

// Client is an OAuth client of one authorization server.
type Client struct {
	ID     string
	Secret string
	// AuthMethod is how the client authenticates at the token endpoint.
	AuthMethod string
	// Registration is how the client came by its id.
	Registration string
}

// Token is what a token endpoint issued.
type Token struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	Scope        string
	// ExpiresAt is zero when the server did not say.
	ExpiresAt time.Time
}

// Error is an OAuth error response (RFC 6749 section 5.2).
type Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
	URI         string `json:"error_uri,omitempty"`
}

// Error returns the code and description.
func (e *Error) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

// PKCE returns a fresh code verifier and its S256 challenge.
func PKCE() (verifier, challenge string, err error) {
	verifier, err = randomString(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// State returns an unguessable state value for one authorization request.
func State() (string, error) { return randomString(32) }

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthorizationRequest is what the browser is sent to the authorization
// server with.
type AuthorizationRequest struct {
	Client      Client
	RedirectURI string
	State       string
	Challenge   string
	Scope       string
	Resource    string
}

// AuthorizationURL builds the URL of an authorization code request with
// PKCE and the RFC 8707 resource.
func AuthorizationURL(meta ServerMetadata, r AuthorizationRequest) (string, error) {
	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization endpoint: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", r.Client.ID)
	q.Set("redirect_uri", r.RedirectURI)
	q.Set("state", r.State)
	q.Set("code_challenge", r.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("resource", r.Resource)
	if r.Scope != "" {
		q.Set("scope", r.Scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CheckIssuer applies RFC 9207 to an authorization response before its code
// is redeemed: an iss that is present must be the issuer the request was
// sent to, and one a server promised must be present.
func CheckIssuer(meta ServerMetadata, iss string, present bool) error {
	switch {
	case present && iss != meta.Issuer:
		return fmt.Errorf("the authorization response came from %q, not from %q, where the request was sent", iss, meta.Issuer)
	case !present && meta.IssParameterSupported:
		return fmt.Errorf("the authorization response from %s is missing its iss parameter", meta.Issuer)
	}
	return nil
}

// Exchange redeems an authorization code.
func Exchange(ctx context.Context, client *http.Client, meta ServerMetadata, c Client, code, verifier, redirectURI, resource string) (Token, error) {
	form := url.Values{
		"grant_type":    {grantCode},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"resource":      {resource},
	}
	return tokenRequest(ctx, client, meta, c, form)
}

// Refresh trades a refresh token for a new access token, for the same
// resource. A server that rotates refresh tokens returns a new one.
func Refresh(ctx context.Context, client *http.Client, meta ServerMetadata, c Client, refreshToken, resource string) (Token, error) {
	form := url.Values{
		"grant_type":    {grantFresh},
		"refresh_token": {refreshToken},
		"resource":      {resource},
	}
	t, err := tokenRequest(ctx, client, meta, c, form)
	if err == nil && t.RefreshToken == "" {
		t.RefreshToken = refreshToken
	}
	return t, err
}

// tokenRequest posts to the token endpoint with the client's credentials.
func tokenRequest(ctx context.Context, client *http.Client, meta ServerMetadata, c Client, form url.Values) (Token, error) {
	var basic bool
	switch c.AuthMethod {
	case AuthBasic:
		basic = true
	case AuthPost:
		form.Set("client_id", c.ID)
		form.Set("client_secret", c.Secret)
	default:
		form.Set("client_id", c.ID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		// RFC 6749 section 2.3.1: both halves are form-encoded first.
		req.SetBasicAuth(url.QueryEscape(c.ID), url.QueryEscape(c.Secret))
	}
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("post to the token endpoint: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDocument))
	if err != nil {
		return Token{}, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var oe Error
		if json.Unmarshal(data, &oe) == nil && oe.Code != "" {
			return Token{}, &oe
		}
		return Token{}, fmt.Errorf("the token endpoint answered HTTP %d", resp.StatusCode)
	}
	var body struct {
		AccessToken  string          `json:"access_token"`
		TokenType    string          `json:"token_type"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
		RefreshToken string          `json:"refresh_token"`
		Scope        string          `json:"scope"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return Token{}, fmt.Errorf("decode token response: %w", err)
	}
	if body.AccessToken == "" {
		return Token{}, errors.New("the token response holds no access token")
	}
	if body.TokenType != "" && !strings.EqualFold(body.TokenType, "bearer") {
		return Token{}, fmt.Errorf("the token endpoint issued a %q token; only bearer tokens are supported", body.TokenType)
	}
	t := Token{AccessToken: body.AccessToken, TokenType: "Bearer", RefreshToken: body.RefreshToken, Scope: body.Scope}
	if seconds := expiresIn(body.ExpiresIn); seconds > 0 {
		t.ExpiresAt = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	}
	return t, nil
}

// expiresIn reads expires_in, which servers send as a number or, against
// the RFC, as a string.
func expiresIn(raw json.RawMessage) int64 {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		if v, err := n.Int64(); err == nil {
			return v
		}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		var v int64
		if _, err := fmt.Sscan(s, &v); err == nil {
			return v
		}
	}
	return 0
}

// Revoke asks the authorization server to forget a token (RFC 7009). A
// server with no revocation endpoint has nothing to be asked.
func Revoke(ctx context.Context, client *http.Client, meta ServerMetadata, c Client, token, hint string) error {
	if meta.RevocationEndpoint == "" || token == "" {
		return nil
	}
	form := url.Values{"token": {token}, "token_type_hint": {hint}}
	if c.AuthMethod != AuthBasic {
		form.Set("client_id", c.ID)
		if c.AuthMethod == AuthPost {
			form.Set("client_secret", c.Secret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RevocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build revocation request: %w", err)
	}
	if c.AuthMethod == AuthBasic {
		req.SetBasicAuth(url.QueryEscape(c.ID), url.QueryEscape(c.Secret))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post to the revocation endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the revocation endpoint answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// Registration is what a client registers itself with.
type Registration struct {
	ClientName  string
	ClientURI   string
	RedirectURI string
	Scope       string
}

// Register registers a client dynamically (RFC 7591) as a public client that
// uses the authorization code grant with PKCE. application_type is native
// for a loopback redirect and web otherwise, which is what an OpenID
// provider checks the redirect URI against.
func Register(ctx context.Context, client *http.Client, meta ServerMetadata, r Registration) (Client, error) {
	if meta.RegistrationEndpoint == "" {
		return Client{}, errors.New("the authorization server offers no dynamic client registration")
	}
	appType := "web"
	if u, err := url.Parse(r.RedirectURI); err == nil && Loopback(u.Hostname()) {
		appType = "native"
	}
	body := map[string]any{
		"client_name":                r.ClientName,
		"redirect_uris":              []string{r.RedirectURI},
		"grant_types":                []string{grantCode, grantFresh},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": AuthNone,
		"application_type":           appType,
	}
	if r.ClientURI != "" {
		body["client_uri"] = r.ClientURI
	}
	if r.Scope != "" {
		body["scope"] = r.Scope
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Client{}, fmt.Errorf("encode registration: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, bytes.NewReader(data))
	if err != nil {
		return Client{}, fmt.Errorf("build registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Client{}, fmt.Errorf("post to the registration endpoint: %w", err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxDocument))
	if err != nil {
		return Client{}, fmt.Errorf("read registration response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var oe Error
		if json.Unmarshal(answer, &oe) == nil && oe.Code != "" {
			return Client{}, fmt.Errorf("register a client: %w", &oe)
		}
		return Client{}, fmt.Errorf("register a client: HTTP %d", resp.StatusCode)
	}
	var reg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthMethod   string `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(answer, &reg); err != nil {
		return Client{}, fmt.Errorf("decode registration response: %w", err)
	}
	if reg.ClientID == "" {
		return Client{}, errors.New("the registration response holds no client_id")
	}
	c := Client{ID: reg.ClientID, Secret: reg.ClientSecret, AuthMethod: reg.AuthMethod, Registration: RegisteredDynamically}
	if c.AuthMethod == "" {
		c.AuthMethod = AuthNone
		if c.Secret != "" {
			c.AuthMethod = SecretMethod(meta)
		}
	}
	return c, nil
}

// SecretMethod picks how a client with a secret authenticates: HTTP Basic,
// which RFC 6749 requires every server to take, unless the server lists only
// the form.
func SecretMethod(meta ServerMetadata) string {
	methods := meta.TokenEndpointAuthMethodsSupported
	if len(methods) > 0 && !slices.Contains(methods, AuthBasic) && slices.Contains(methods, AuthPost) {
		return AuthPost
	}
	return AuthBasic
}

// MetadataDocument is the Client ID Metadata Document of a client whose id
// is the document's own URL.
func MetadataDocument(clientID, name, clientURI string, redirectURIs []string) map[string]any {
	doc := map[string]any{
		"client_id":                  clientID,
		"client_name":                name,
		"redirect_uris":              redirectURIs,
		"grant_types":                []string{grantCode, grantFresh},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": AuthNone,
	}
	if clientURI != "" {
		doc["client_uri"] = clientURI
	}
	return doc
}
