package oauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/mcp/mcptest"
	"github.com/erlidev/eika/internal/mcp/oauth"
)

func TestParseChallenge(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   oauth.Challenge
	}{
		{
			name:   "resource metadata and scope",
			values: []string{`Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource", scope="files:read"`},
			want:   oauth.Challenge{ResourceMetadata: "https://mcp.example.com/.well-known/oauth-protected-resource", Scope: "files:read"},
		},
		{
			name:   "insufficient scope with a description",
			values: []string{`Bearer error="insufficient_scope", scope="files:write", error_description="File write permission required"`},
			want:   oauth.Challenge{Error: "insufficient_scope", Scope: "files:write", ErrorDescription: "File write permission required"},
		},
		{
			name:   "after another scheme in the same value",
			values: []string{`Basic realm="x", Bearer realm="mcp", scope="a b"`},
			want:   oauth.Challenge{Scope: "a b"},
		},
		{
			name:   "in a second header value",
			values: []string{`Basic realm="x"`, `bearer error=invalid_token`},
			want:   oauth.Challenge{Error: "invalid_token"},
		},
		{
			name:   "an unquoted URL",
			values: []string{`Bearer resource_metadata=https://mcp.example.com/prm`},
			want:   oauth.Challenge{ResourceMetadata: "https://mcp.example.com/prm"},
		},
		{
			name:   "escaped quotes",
			values: []string{`Bearer error_description="say \"hi\""`},
			want:   oauth.Challenge{ErrorDescription: `say "hi"`},
		},
		{name: "no bearer challenge", values: []string{`Basic realm="x"`}, want: oauth.Challenge{}},
		{name: "a bare scheme", values: []string{`Bearer`}, want: oauth.Challenge{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := oauth.ParseChallenge(tt.values); got != tt.want {
				t.Errorf("ParseChallenge = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// resourceServer serves an MCP endpoint's protected resource metadata at
// the given paths and nothing else, and counts the paths asked for.
func resourceServer(t *testing.T, auth *mcptest.Authorization, paths ...string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		for _, p := range paths {
			if r.URL.Path == p {
				auth.ProtectedResource(srv.URL+"/tenant/mcp", "tools:read")(w, r)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestDiscoverFollowsTheChallengeFirst(t *testing.T) {
	auth := mcptest.NewAuthorization(t)
	srv, asked := resourceServer(t, auth, "/custom/prm")
	d, err := oauth.Discover(context.Background(), http.DefaultClient, srv.URL+"/tenant/mcp", oauth.Challenge{ResourceMetadata: srv.URL + "/custom/prm"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if d.ResourceMetadataURL != srv.URL+"/custom/prm" || d.Server.Issuer != auth.Issuer {
		t.Errorf("discovery = %+v", d)
	}
	if d.Resource != srv.URL+"/tenant/mcp" {
		t.Errorf("resource = %q, want the metadata's", d.Resource)
	}
	if (*asked)[0] != "/custom/prm" {
		t.Errorf("asked %v first, want the challenge's URL", *asked)
	}
}

func TestDiscoverTriesTheWellKnownURIsInOrder(t *testing.T) {
	tests := []struct {
		name  string
		serve string
		want  []string
	}{
		{"under the endpoint's path", "/.well-known/oauth-protected-resource/tenant/mcp",
			[]string{"/.well-known/oauth-protected-resource/tenant/mcp"}},
		{"at the root", "/.well-known/oauth-protected-resource",
			[]string{"/.well-known/oauth-protected-resource/tenant/mcp", "/.well-known/oauth-protected-resource"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := mcptest.NewAuthorization(t)
			srv, asked := resourceServer(t, auth, tt.serve)
			d, err := oauth.Discover(context.Background(), http.DefaultClient, srv.URL+"/tenant/mcp", oauth.Challenge{})
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if strings.Join(*asked, " ") != strings.Join(tt.want, " ") {
				t.Errorf("asked %v, want %v", *asked, tt.want)
			}
			if d.Server.TokenEndpoint != auth.Issuer+"/token" {
				t.Errorf("token endpoint = %q", d.Server.TokenEndpoint)
			}
		})
	}
}

func TestDiscoverFallsBackToTheServersOrigin(t *testing.T) {
	// A 2025-03-26 server: no resource metadata; its origin is its own
	// authorization server and publishes nothing either.
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	d, err := oauth.Discover(context.Background(), http.DefaultClient, srv.URL+"/mcp", oauth.Challenge{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !d.Defaulted || d.Server.AuthorizationEndpoint != srv.URL+"/authorize" || d.Server.RegistrationEndpoint != srv.URL+"/register" {
		t.Errorf("discovery = %+v, want the default endpoints", d)
	}
}

func TestServerMetadataURLs(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	if _, ok, err := oauth.ServerMetadataOf(context.Background(), http.DefaultClient, srv.URL+"/tenant1"); err != nil || ok {
		t.Fatalf("ServerMetadataOf = %v, %v; want nothing found", ok, err)
	}
	want := []string{
		"/.well-known/oauth-authorization-server/tenant1",
		"/.well-known/openid-configuration/tenant1",
		"/tenant1/.well-known/openid-configuration",
	}
	if strings.Join(asked, " ") != strings.Join(want, " ") {
		t.Errorf("asked %v, want %v", asked, want)
	}
}

func TestDiscoverRefusesUnsafeServers(t *testing.T) {
	tests := []struct {
		name     string
		metadata func(issuer string) map[string]any
		want     string
	}{
		{"an issuer that is not the one asked", func(string) map[string]any {
			return map[string]any{"issuer": "https://honest.example", "authorization_endpoint": "https://honest.example/a", "token_endpoint": "https://honest.example/t", "code_challenge_methods_supported": []string{"S256"}}
		}, "names issuer"},
		{"no PKCE", func(issuer string) map[string]any {
			return map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/a", "token_endpoint": issuer + "/t"}
		}, "PKCE"},
		{"a token endpoint that is not https", func(issuer string) map[string]any {
			return map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/a", "token_endpoint": "http://auth.example.com/t", "code_challenge_methods_supported": []string{"S256"}}
		}, "not https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": srv.URL + "/mcp", "authorization_servers": []string{srv.URL}})
				case "/.well-known/oauth-authorization-server":
					_ = json.NewEncoder(w).Encode(tt.metadata(srv.URL))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			_, err := oauth.Discover(context.Background(), http.DefaultClient, srv.URL+"/mcp", oauth.Challenge{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Discover error = %v, want one saying %q", err, tt.want)
			}
		})
	}
}

func TestDiscoverRefusesMetadataForAnotherResource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": "https://elsewhere.example/mcp", "authorization_servers": []string{"https://auth.example"}})
	}))
	t.Cleanup(srv.Close)
	_, err := oauth.Discover(context.Background(), http.DefaultClient, srv.URL+"/mcp", oauth.Challenge{})
	if err == nil || !strings.Contains(err.Error(), "not for") {
		t.Errorf("Discover error = %v, want a refusal of the other resource", err)
	}
}

func TestCheckIssuer(t *testing.T) {
	promised := oauth.ServerMetadata{Issuer: "https://auth.example", IssParameterSupported: true}
	silent := oauth.ServerMetadata{Issuer: "https://auth.example"}
	tests := []struct {
		name    string
		meta    oauth.ServerMetadata
		iss     string
		present bool
		ok      bool
	}{
		{"promised and matching", promised, "https://auth.example", true, true},
		{"promised and missing", promised, "", false, false},
		{"promised and another", promised, "https://evil.example", true, false},
		{"not promised but sent", silent, "https://auth.example", true, true},
		{"not promised, sent, and another", silent, "https://evil.example", true, false},
		{"not promised and missing", silent, "", false, true},
		{"no normalization of a trailing slash", promised, "https://auth.example/", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := oauth.CheckIssuer(tt.meta, tt.iss, tt.present)
			if (err == nil) != tt.ok {
				t.Errorf("CheckIssuer = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

func TestAuthorizationCodeFlow(t *testing.T) {
	ctx := context.Background()
	auth := mcptest.NewAuthorization(t)
	meta, ok, err := oauth.ServerMetadataOf(ctx, http.DefaultClient, auth.Issuer)
	if err != nil || !ok {
		t.Fatalf("ServerMetadataOf = %v, %v", ok, err)
	}
	redirect := "http://127.0.0.1:8080/mcp/callback"
	client, err := oauth.Register(ctx, http.DefaultClient, meta, oauth.Registration{ClientName: "Eika", RedirectURI: redirect})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if reg := auth.Registrations()[0]; reg["application_type"] != "native" || reg["token_endpoint_auth_method"] != "none" {
		t.Errorf("registration = %v, want a native public client", reg)
	}
	verifier, challenge, err := oauth.PKCE()
	if err != nil {
		t.Fatalf("PKCE: %v", err)
	}
	target, err := oauth.AuthorizationURL(meta, oauth.AuthorizationRequest{
		Client: client, RedirectURI: redirect, State: "s1", Challenge: challenge, Scope: "tools:read", Resource: "https://mcp.example/mcp",
	})
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	back, err := auth.Approve(ctx, target)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if back.Query().Get("state") != "s1" {
		t.Fatalf("callback %s lost the state", back)
	}
	token, err := oauth.Exchange(ctx, http.DefaultClient, meta, client, back.Query().Get("code"), verifier, redirect, "https://mcp.example/mcp")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if !auth.Valid("Bearer "+token.AccessToken, "https://mcp.example/mcp") || token.RefreshToken == "" || token.ExpiresAt.IsZero() {
		t.Errorf("token = %+v", token)
	}
	refreshed, err := oauth.Refresh(ctx, http.DefaultClient, meta, client, token.RefreshToken, "https://mcp.example/mcp")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.AccessToken == token.AccessToken || refreshed.RefreshToken == token.RefreshToken {
		t.Errorf("refresh did not rotate: %+v", refreshed)
	}
	for _, form := range auth.TokenRequests() {
		if form.Get("resource") != "https://mcp.example/mcp" || form.Get("client_id") != client.ID {
			t.Errorf("token request %v lacks the resource or the client", form)
		}
	}
	// The old refresh token was rotated away.
	var oe *oauth.Error
	if _, err := oauth.Refresh(ctx, http.DefaultClient, meta, client, token.RefreshToken, "https://mcp.example/mcp"); !errors.As(err, &oe) || oe.Code != "invalid_grant" {
		t.Errorf("reused refresh token: %v, want invalid_grant", err)
	}
}

func TestExchangeRefusesAWrongVerifier(t *testing.T) {
	ctx := context.Background()
	auth := mcptest.NewAuthorization(t)
	meta, _, _ := oauth.ServerMetadataOf(ctx, http.DefaultClient, auth.Issuer)
	redirect := "https://eika.example/mcp/callback"
	client, _ := oauth.Register(ctx, http.DefaultClient, meta, oauth.Registration{ClientName: "Eika", RedirectURI: redirect})
	if reg := auth.Registrations()[0]; reg["application_type"] != "web" {
		t.Errorf("application_type = %v for a remote redirect, want web", reg["application_type"])
	}
	_, challenge, _ := oauth.PKCE()
	target, _ := oauth.AuthorizationURL(meta, oauth.AuthorizationRequest{Client: client, RedirectURI: redirect, State: "s", Challenge: challenge, Resource: "https://r.example"})
	back, err := auth.Approve(ctx, target)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	other, _, _ := oauth.PKCE()
	if _, err := oauth.Exchange(ctx, http.DefaultClient, meta, client, back.Query().Get("code"), other, redirect, "https://r.example"); err == nil {
		t.Error("Exchange accepted a verifier that does not match the challenge")
	}
}

func TestSecretMethod(t *testing.T) {
	tests := []struct {
		methods []string
		want    string
	}{
		{nil, oauth.AuthBasic},
		{[]string{oauth.AuthPost}, oauth.AuthPost},
		{[]string{oauth.AuthPost, oauth.AuthBasic}, oauth.AuthBasic},
	}
	for _, tt := range tests {
		if got := oauth.SecretMethod(oauth.ServerMetadata{TokenEndpointAuthMethodsSupported: tt.methods}); got != tt.want {
			t.Errorf("SecretMethod(%v) = %s, want %s", tt.methods, got, tt.want)
		}
	}
}

func TestCanonicalResource(t *testing.T) {
	for raw, want := range map[string]string{
		"HTTPS://MCP.Example.com/mcp/":  "https://mcp.example.com/mcp",
		"https://mcp.example.com":       "https://mcp.example.com",
		"https://mcp.example.com:8443":  "https://mcp.example.com:8443",
		"https://mcp.example.com/a?b#c": "https://mcp.example.com/a",
	} {
		u, _ := url.Parse(raw)
		if got := oauth.CanonicalResource(u); got != want {
			t.Errorf("CanonicalResource(%s) = %s, want %s", raw, got, want)
		}
	}
}
