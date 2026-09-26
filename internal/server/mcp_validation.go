package server

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/store"
)

// Bounds on what the MCP routes accept. A server name is part of every tool
// name its server offers, which Chat Completions cuts at 64 characters, so
// it is kept short enough to leave the tool's own name room.
const (
	maxMCPServerName  = 32
	maxMCPURL         = 2048
	maxMCPCommand     = 1024
	maxMCPArgs        = 64
	maxMCPArg         = 4096
	maxMCPPairs       = 64
	maxMCPPairName    = 256
	maxMCPPairValue   = 8192
	maxMCPToolList    = 512
	maxMCPToolName    = 256
	maxOAuthClientID  = 512
	maxOAuthSecret    = 4096
	maxMCPResourceURI = 4096
	maxMCPPromptArgs  = 64
)

// mcpServerNamePattern is what a server may be called: letters, digits,
// hyphens, and single underscores between them, so that the `__` in
// mcp__<server>__<tool> always marks where the server's name ends.
var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]+(_[A-Za-z0-9-]+)*$`)

// envNamePattern is what an environment variable may be called.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// headerNamePattern is an HTTP field name, RFC 9110's token.
var headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// clientHeaders are the headers the MCP client sets itself, which a server's
// configuration may not replace.
var clientHeaders = []string{
	"accept", "connection", "content-length", "content-type", "host", "last-event-id",
	"mcp-method", "mcp-name", "mcp-protocol-version", "mcp-session-id", "transfer-encoding",
}

// validateMCPServer checks a server's configuration before it is stored.
func validateMCPServer(row store.MCPServer, headers, env map[string]string, clientSecret string) error {
	switch {
	case row.Name == "":
		return invalidf("name is required")
	case len(row.Name) > maxMCPServerName:
		return invalidf("name must be at most %d characters", maxMCPServerName)
	case !mcpServerNamePattern.MatchString(row.Name):
		return invalidf("name %q may hold letters, digits, hyphens, and single underscores between them, since it becomes part of the name of every tool the server offers", row.Name)
	case len(row.DisabledTools) > maxMCPToolList:
		return invalidf("disabled_tools may name at most %d tools", maxMCPToolList)
	}
	for _, name := range row.DisabledTools {
		if len(name) > maxMCPToolName {
			return invalidf("a disabled tool's name must be at most %d bytes", maxMCPToolName)
		}
	}
	switch row.Kind {
	case mcp.KindHTTP:
		if row.Command != "" || len(row.Args) > 0 || len(env) > 0 {
			return invalidf("an http server has a url, not a command, args, or env")
		}
		if err := validateMCPURL(row.URL); err != nil {
			return err
		}
		if err := validateHeaders(headers); err != nil {
			return err
		}
		if len(row.OAuthClientID) > maxOAuthClientID || len(clientSecret) > maxOAuthSecret {
			return invalidf("oauth_client_id must be at most %d bytes and oauth_client_secret at most %d", maxOAuthClientID, maxOAuthSecret)
		}
		if clientSecret != "" && row.OAuthClientID == "" {
			return invalidf("an oauth_client_secret needs the oauth_client_id it belongs to")
		}
	case mcp.KindStdio:
		if row.URL != "" || len(headers) > 0 || row.OAuthClientID != "" || clientSecret != "" {
			return invalidf("a stdio server has a command, not a url, headers, or an OAuth client; it takes its credentials from env")
		}
		if err := validateCommand(row.Command, row.Args); err != nil {
			return err
		}
		if err := validateEnv(env); err != nil {
			return err
		}
	default:
		return invalidf("kind must be %q or %q", mcp.KindHTTP, mcp.KindStdio)
	}
	return nil
}

// validateMCPURL checks a remote server's address.
func validateMCPURL(raw string) error {
	if raw == "" {
		return invalidf("url is required for an http server")
	}
	if len(raw) > maxMCPURL {
		return invalidf("url must be at most %d bytes", maxMCPURL)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidf("url must be an http or https URL such as https://mcp.example.com/mcp")
	}
	if u.User != nil {
		return invalidf("url must not contain credentials; put them in headers")
	}
	if u.Fragment != "" {
		return invalidf("url must not have a fragment")
	}
	return nil
}

// validateHeaders checks the headers a remote server's requests carry.
func validateHeaders(headers map[string]string) error {
	if len(headers) > maxMCPPairs {
		return invalidf("a server may have at most %d headers", maxMCPPairs)
	}
	for name, value := range headers {
		lower := strings.ToLower(name)
		switch {
		case !headerNamePattern.MatchString(name) || len(name) > maxMCPPairName:
			return invalidf("header %q is not a valid header name", name)
		case slices.Contains(clientHeaders, lower) || strings.HasPrefix(lower, "mcp-param-"):
			return invalidf("header %s is set by the MCP client itself", name)
		case len(value) > maxMCPPairValue || strings.ContainsAny(value, "\r\n\x00"):
			return invalidf("the value of header %s must be one line of at most %d bytes", name, maxMCPPairValue)
		}
	}
	return nil
}

// validateCommand checks a stdio server's command line.
func validateCommand(command string, args []string) error {
	if command == "" {
		return invalidf("command is required for a stdio server")
	}
	if len(command) > maxMCPCommand || strings.ContainsRune(command, 0) {
		return invalidf("command must be at most %d bytes", maxMCPCommand)
	}
	if len(args) > maxMCPArgs {
		return invalidf("a stdio server takes at most %d args", maxMCPArgs)
	}
	for _, arg := range args {
		if len(arg) > maxMCPArg || strings.ContainsRune(arg, 0) {
			return invalidf("an arg must be at most %d bytes", maxMCPArg)
		}
	}
	return nil
}

// validateEnv checks a stdio server's environment.
func validateEnv(env map[string]string) error {
	if len(env) > maxMCPPairs {
		return invalidf("a server may have at most %d environment variables", maxMCPPairs)
	}
	for name, value := range env {
		if !envNamePattern.MatchString(name) || len(name) > maxMCPPairName {
			return invalidf("%q is not a valid environment variable name", name)
		}
		if len(value) > maxMCPPairValue || strings.ContainsRune(value, 0) {
			return invalidf("the value of %s must be at most %d bytes", name, maxMCPPairValue)
		}
	}
	return nil
}

// toolList is a disabled_tools list as it is stored: trimmed, without
// blanks or repeats, sorted.
func toolList(names []string) []string {
	out := []string{}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
