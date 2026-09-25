// Package oauth is the client side of MCP authorization: OAuth 2.1 with the
// discovery and registration mechanisms the MCP authorization spec names.
//
// A server that refuses a request answers 401 with a WWW-Authenticate Bearer
// challenge, which ParseChallenge reads. Discover follows it to the
// server's protected resource metadata (RFC 9728) and on to its
// authorization server's metadata (RFC 8414, or OpenID Connect discovery),
// and refuses a server that does not offer PKCE with S256. A client is
// registered by hand, by a Client ID Metadata Document (MetadataDocument),
// or dynamically (Register, RFC 7591). AuthorizationURL, CheckIssuer
// (RFC 9207), Exchange, Refresh, and Revoke are the authorization code flow
// itself, every request naming the resource (RFC 8707).
//
// The package keeps no state: where a pending authorization, a client, and
// its tokens are kept is the caller's business. It depends only on the
// standard library.
package oauth
