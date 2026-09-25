package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// maxDocument bounds a metadata document or a token endpoint's answer.
const maxDocument = 1 << 20

// ResourceMetadata is an MCP server's OAuth 2.0 Protected Resource Metadata
// (RFC 9728): which authorization servers issue its tokens.
type ResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
	ResourceName           string   `json:"resource_name,omitempty"`
	ResourceDocumentation  string   `json:"resource_documentation,omitempty"`
}

// ServerMetadata is an authorization server's metadata (RFC 8414, or
// OpenID Connect Discovery), limited to what the client uses.
type ServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	ClientIDMetadataDocumentSupported bool     `json:"client_id_metadata_document_supported,omitempty"`
	// IssParameterSupported is RFC 9207's
	// authorization_response_iss_parameter_supported.
	IssParameterSupported bool `json:"authorization_response_iss_parameter_supported,omitempty"`
}

// Discovery is everything the client learned before it can ask for a token:
// the resource the token is for and the authorization server to ask.
type Discovery struct {
	// Resource is the canonical URI of the MCP server, the RFC 8707 resource
	// every authorization and token request names.
	Resource string
	// ResourceMetadataURL is where the resource metadata came from, empty
	// when the server published none and its origin was taken for the
	// authorization server, as the 2025-03-26 revision did.
	ResourceMetadataURL string
	ResourceMetadata    ResourceMetadata
	Server              ServerMetadata
	// Defaulted reports that the server published no metadata at all and
	// the 2025-03-26 default endpoints are in use.
	Defaulted bool
}

// Discover finds the authorization server of the MCP server at serverURL,
// starting from what its challenge said. It follows the order the MCP
// authorization spec gives: the challenge's resource_metadata, then the
// well-known URIs with and without the endpoint's path; then, for each
// candidate issuer URL, RFC 8414 metadata before OpenID Connect discovery.
func Discover(ctx context.Context, client *http.Client, serverURL string, ch Challenge) (Discovery, error) {
	server, err := url.Parse(serverURL)
	if err != nil || server.Host == "" {
		return Discovery{}, fmt.Errorf("parse server URL %q: not an absolute URL", serverURL)
	}
	d := Discovery{Resource: CanonicalResource(server)}
	candidates := resourceMetadataURLs(server)
	if ch.ResourceMetadata != "" {
		// The challenge's URL comes first; the well-known ones still answer
		// for a server whose challenge points at a document it lost.
		candidates = append([]string{ch.ResourceMetadata}, candidates...)
	}
	found := false
	for _, candidate := range candidates {
		var prm ResourceMetadata
		ok, err := getJSON(ctx, client, candidate, &prm)
		if err != nil {
			return Discovery{}, fmt.Errorf("read protected resource metadata: %w", err)
		}
		if !ok {
			continue
		}
		if err := checkResource(server, prm.Resource); err != nil {
			return Discovery{}, err
		}
		if len(prm.AuthorizationServers) == 0 {
			return Discovery{}, fmt.Errorf("the protected resource metadata at %s names no authorization server", candidate)
		}
		d.ResourceMetadataURL, d.ResourceMetadata, found = candidate, prm, true
		if prm.Resource != "" {
			d.Resource = prm.Resource
		}
		break
	}

	issuer := ""
	if found {
		// RFC 9728 leaves the choice among several to the client; the first
		// is the one the server lists first.
		issuer = d.ResourceMetadata.AuthorizationServers[0]
	} else {
		// A server from before protected resource metadata was its own
		// authorization server, at its origin.
		issuer = (&url.URL{Scheme: server.Scheme, Host: server.Host}).String()
	}
	meta, ok, err := ServerMetadataOf(ctx, client, issuer)
	if err != nil {
		return Discovery{}, err
	}
	if !ok {
		if found {
			return Discovery{}, fmt.Errorf("the authorization server %s publishes no metadata", issuer)
		}
		// The 2025-03-26 revision's defaults, for a server that published
		// nothing at all.
		meta = ServerMetadata{
			Issuer:                        issuer,
			AuthorizationEndpoint:         issuer + "/authorize",
			TokenEndpoint:                 issuer + "/token",
			RegistrationEndpoint:          issuer + "/register",
			CodeChallengeMethodsSupported: []string{"S256"},
		}
		d.Defaulted = true
	}
	if err := checkServerMetadata(meta); err != nil {
		return Discovery{}, err
	}
	d.Server = meta
	return d, nil
}

// ServerMetadataOf fetches an authorization server's metadata from the
// well-known URIs of its issuer, in the spec's order. It reports false when
// none of them has any.
func ServerMetadataOf(ctx context.Context, client *http.Client, issuer string) (ServerMetadata, bool, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return ServerMetadata{}, false, fmt.Errorf("the authorization server %q is not an issuer URL", issuer)
	}
	if err := checkEndpoint("issuer", issuer); err != nil {
		return ServerMetadata{}, false, err
	}
	for _, candidate := range serverMetadataURLs(u) {
		var meta ServerMetadata
		ok, err := getJSON(ctx, client, candidate, &meta)
		if err != nil {
			return ServerMetadata{}, false, fmt.Errorf("read authorization server metadata: %w", err)
		}
		if !ok {
			continue
		}
		// RFC 8414 section 3.3: a document that names another issuer than
		// the one its URL was built from is not to be used.
		if meta.Issuer != issuer {
			return ServerMetadata{}, false, fmt.Errorf("the metadata at %s names issuer %q, not %q", candidate, meta.Issuer, issuer)
		}
		return meta, true, nil
	}
	return ServerMetadata{}, false, nil
}

// resourceMetadataURLs are the well-known places an MCP endpoint's resource
// metadata may be: under its path, then at the root.
func resourceMetadataURLs(server *url.URL) []string {
	root := &url.URL{Scheme: server.Scheme, Host: server.Host, Path: "/.well-known/oauth-protected-resource"}
	path := strings.TrimSuffix(server.EscapedPath(), "/")
	if path == "" {
		return []string{root.String()}
	}
	withPath := *root
	withPath.Path += path
	return []string{withPath.String(), root.String()}
}

// serverMetadataURLs are the well-known places of an issuer's metadata.
func serverMetadataURLs(issuer *url.URL) []string {
	origin := (&url.URL{Scheme: issuer.Scheme, Host: issuer.Host}).String()
	path := strings.TrimSuffix(issuer.EscapedPath(), "/")
	if path == "" {
		return []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}
	}
	return []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + "/.well-known/openid-configuration" + path,
		origin + path + "/.well-known/openid-configuration",
	}
}

// CanonicalResource is the RFC 8707 resource identifier of an endpoint: its
// scheme and host in lower case, its path without a trailing slash, and no
// query or fragment.
func CanonicalResource(u *url.URL) string {
	c := url.URL{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Host), Path: strings.TrimSuffix(u.Path, "/")}
	return c.String()
}

// checkResource accepts resource metadata that is about the server asked:
// the same origin, and the server's path within the resource's.
func checkResource(server *url.URL, resource string) error {
	if resource == "" {
		return errors.New("the protected resource metadata names no resource")
	}
	r, err := url.Parse(resource)
	if err != nil || !strings.EqualFold(r.Scheme, server.Scheme) || !strings.EqualFold(r.Host, server.Host) {
		return fmt.Errorf("the protected resource metadata is for %q, not for %s", resource, CanonicalResource(server))
	}
	want := strings.TrimSuffix(r.Path, "/")
	have := strings.TrimSuffix(server.Path, "/")
	if have != want && !strings.HasPrefix(have, want+"/") {
		return fmt.Errorf("the protected resource metadata is for %q, not for %s", resource, CanonicalResource(server))
	}
	return nil
}

// checkServerMetadata accepts metadata the client can use safely: the
// endpoints it needs, on https, and PKCE with S256.
func checkServerMetadata(m ServerMetadata) error {
	if m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" {
		return fmt.Errorf("the authorization server %s names no authorization or token endpoint", m.Issuer)
	}
	for name, endpoint := range map[string]string{
		"authorization endpoint": m.AuthorizationEndpoint,
		"token endpoint":         m.TokenEndpoint,
		"registration endpoint":  m.RegistrationEndpoint,
		"revocation endpoint":    m.RevocationEndpoint,
	} {
		if endpoint == "" {
			continue
		}
		if err := checkEndpoint(name, endpoint); err != nil {
			return err
		}
	}
	// A server that does not say it supports PKCE is taken not to, and the
	// spec forbids going on without it.
	if !slices.Contains(m.CodeChallengeMethodsSupported, "S256") {
		return fmt.Errorf("the authorization server %s does not advertise PKCE with S256, which MCP authorization requires", m.Issuer)
	}
	return nil
}

// checkEndpoint accepts an https URL, or an http one on the loopback
// interface, where a developer runs an authorization server of their own.
func checkEndpoint(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("the %s %q is not an absolute URL", name, raw)
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && Loopback(u.Hostname()):
		return nil
	}
	return fmt.Errorf("the %s %q is not https, which OAuth requires", name, raw)
}

// Loopback reports whether host names the local machine.
func Loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// getJSON fetches a metadata document. It reports false for a 404, or any
// 4xx a server answers a well-known URI it does not serve with, and an error
// for anything else that is not a JSON document.
func getJSON(ctx context.Context, client *http.Client, target string, into any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, fmt.Errorf("build request for %s: %w", target, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("get %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("get %s: HTTP %d", target, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDocument+1))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", target, err)
	}
	if len(data) > maxDocument {
		return false, fmt.Errorf("the document at %s is larger than %d bytes", target, maxDocument)
	}
	if err := json.Unmarshal(data, into); err != nil {
		ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if ct == "application/json" || strings.HasSuffix(ct, "+json") {
			return false, fmt.Errorf("decode %s: %w", target, err)
		}
		// Something that is not a document at all, such as the HTML a
		// single-page app answers every path with.
		return false, nil
	}
	return true, nil
}
