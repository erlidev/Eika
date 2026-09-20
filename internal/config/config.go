package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the full configuration of the Eika harness.
type Config struct {
	// Listen is the address the harness HTTP server binds to.
	Listen string `yaml:"listen"`
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string `yaml:"database_url"`
	// DockerSocket is the path to the Docker socket used to run sandboxes.
	DockerSocket string `yaml:"docker_socket"`
	// SearxNGURL is the base URL of the SearXNG instance used for web search.
	SearxNGURL string `yaml:"searxng_url"`
	// SandboxImage is the image used by workspaces that do not override it.
	SandboxImage string `yaml:"sandbox_image"`
	// SandboxNetwork is the Docker network sandbox containers join, which is
	// how the harness resolves them by container name. Empty publishes each
	// sandbox's daemon port on 127.0.0.1 instead, which is what a harness
	// running outside compose needs.
	SandboxNetwork string `yaml:"sandbox_network"`
	// EikadBinary is the path to the static eikad binary in the harness
	// filesystem. It is copied into every sandbox container.
	EikadBinary string `yaml:"eikad_binary"`
	// HubRoot is the directory holding the hub's bare git repositories.
	HubRoot string `yaml:"hub_root"`
	// HubURL is the harness base URL a sandbox reaches the git hub on.
	HubURL string `yaml:"hub_url"`
	// SecretKeyFile holds the key that seals the credentials the user enters
	// in the UI before they reach the database. It is created on first start,
	// so it lives in the harness's own volume, apart from the database.
	SecretKeyFile string `yaml:"secret_key_file"`
	// AuthToken is an optional fixed API token, accepted as a bearer token
	// beside the sessions a password sign-in creates. It is for scripts and
	// development; a deployment the UI sets up needs none.
	AuthToken string `yaml:"auth_token"`
	// AllowedOrigins lists the browser origins that may open the event
	// stream, on top of the harness's own. Each entry is a host pattern
	// (`localhost:5173`, `*.example.com`); a full URL is accepted and
	// reduced to its host. Same-origin requests are always allowed, so a
	// deployment that serves the frontend itself needs none of these.
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// movedToUI names the file keys earlier versions read that are now settings
// in the web UI, with where to find each. A file that still sets one fails
// with that pointer rather than a bare unknown-field error.
var movedToUI = map[string]string{
	"models":    "models are added in the web UI under Settings, Models",
	"subagents": "subagent limits are set in the web UI under Settings, General",
}

// ErrNoConfigFile reports that a named configuration file does not exist.
var ErrNoConfigFile = errors.New("config file not found")

// Default returns the configuration used when nothing else is set. It is the
// configuration of the harness container in the compose stack, so that stack
// needs no configuration file and no environment beyond the database URL.
func Default() Config {
	return Config{
		Listen:       ":8080",
		DatabaseURL:  "postgres://eika:eika@postgres:5432/eika?sslmode=disable",
		DockerSocket: "/var/run/docker.sock",
		SearxNGURL:   "http://searxng:8080",
		SandboxImage: "eika-sandbox:latest",
		// Sandboxes get their own network, which the harness joins as well.
		// They can therefore reach the harness and be reached by it, but not
		// the database or the search service on the default network.
		SandboxNetwork: "eika_sandbox",
		EikadBinary:    "/usr/local/share/eika/eikad",
		HubRoot:        "/var/lib/eika/hub",
		HubURL:         "http://eika:8080",
		SecretKeyFile:  "/var/lib/eika/secret.key",
	}
}

// Load builds a Config from the defaults, the YAML file at path, and the
// environment, in that order, and validates the result. An empty path skips
// the file; a named file that does not exist is an error wrapping
// ErrNoConfigFile.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("read config %s: %w", path, ErrNoConfigFile)
			}
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := checkMovedKeys(data); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		// An empty or comment-only file decodes to io.EOF; that means the
		// deployment is happy with the defaults.
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	// The harness's own origin is always allowed, whatever else is
	// configured, so that serving the frontend from the harness needs no
	// configuration at all.
	cfg.AllowedOrigins = append(originsOf(cfg.Listen), cfg.AllowedOrigins...)
	for i, origin := range cfg.AllowedOrigins {
		cfg.AllowedOrigins[i] = originHost(origin)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// checkMovedKeys rejects a file that sets a key the web UI owns now.
func checkMovedKeys(data []byte) error {
	var keys map[string]any
	if err := yaml.Unmarshal(data, &keys); err != nil {
		// The strict decode that follows reports a malformed file better.
		return nil
	}
	for key, where := range movedToUI {
		if _, ok := keys[key]; ok {
			return fmt.Errorf("%s is no longer read from the file: %s; remove it", key, where)
		}
	}
	return nil
}

// applyEnv overlays the EIKA_* environment variables onto c.
func (c *Config) applyEnv() {
	overrides := map[string]*string{
		"EIKA_LISTEN":          &c.Listen,
		"EIKA_DATABASE_URL":    &c.DatabaseURL,
		"EIKA_DOCKER_SOCKET":   &c.DockerSocket,
		"EIKA_SEARXNG_URL":     &c.SearxNGURL,
		"EIKA_SANDBOX_IMAGE":   &c.SandboxImage,
		"EIKA_SANDBOX_NETWORK": &c.SandboxNetwork,
		"EIKA_EIKAD_BINARY":    &c.EikadBinary,
		"EIKA_HUB_ROOT":        &c.HubRoot,
		"EIKA_HUB_URL":         &c.HubURL,
		"EIKA_AUTH_TOKEN":      &c.AuthToken,
		"EIKA_SECRET_KEY_FILE": &c.SecretKeyFile,
	}
	for name, field := range overrides {
		if v, ok := os.LookupEnv(name); ok {
			*field = v
		}
	}
	// A list does not fit an environment variable cleanly, but this one is
	// short and a deployment needs it before it has a configuration file.
	if v, ok := os.LookupEnv("EIKA_ALLOWED_ORIGINS"); ok {
		c.AllowedOrigins = nil
		for _, origin := range strings.Split(v, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, origin)
			}
		}
	}
}

// originsOf returns the browser origins that reach a harness listening on
// addr. An address with no host is reachable on loopback under either name.
func originsOf(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return []string{net.JoinHostPort("localhost", port), net.JoinHostPort("127.0.0.1", port)}
	default:
		return []string{net.JoinHostPort(host, port)}
	}
}

// originHost reduces an origin to the host pattern the event stream matches
// against, so that a deployment may write either a host or a whole URL.
func originHost(origin string) string {
	if !strings.Contains(origin, "://") {
		return strings.TrimSuffix(origin, "/")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return origin
	}
	return u.Host
}

// Validate reports whether the configuration is usable.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("validate config: listen is empty")
	}
	if c.DatabaseURL == "" {
		return errors.New("validate config: database_url is empty")
	}
	if c.DockerSocket == "" {
		return errors.New("validate config: docker_socket is empty")
	}
	if c.SearxNGURL == "" {
		return errors.New("validate config: searxng_url is empty")
	}
	if c.SandboxImage == "" {
		return errors.New("validate config: sandbox_image is empty")
	}
	// sandbox_network may be empty: that is the development mode where the
	// harness reaches sandboxes on published loopback ports.
	if c.EikadBinary == "" {
		return errors.New("validate config: eikad_binary is empty")
	}
	if c.HubRoot == "" {
		return errors.New("validate config: hub_root is empty")
	}
	if c.HubURL == "" {
		return errors.New("validate config: hub_url is empty")
	}
	if c.SecretKeyFile == "" {
		return errors.New("validate config: secret_key_file is empty")
	}
	return nil
}

// redacted is the placeholder that replaces a secret in String output.
const redacted = "[REDACTED]"

// String renders the configuration with secrets replaced by a placeholder so
// that it is safe to log.
func (c Config) String() string {
	return fmt.Sprintf("config{listen=%s database_url=%s docker_socket=%s searxng_url=%s sandbox_image=%s sandbox_network=%s eikad_binary=%s hub_root=%s hub_url=%s secret_key_file=%s auth_token=%s allowed_origins=%s}",
		c.Listen, redactURL(c.DatabaseURL), c.DockerSocket, c.SearxNGURL, c.SandboxImage,
		c.SandboxNetwork, c.EikadBinary, c.HubRoot, c.HubURL, c.SecretKeyFile,
		redact(c.AuthToken), strings.Join(c.AllowedOrigins, ","))
}

// redact hides a secret while preserving whether it was set at all.
func redact(secret string) string {
	if secret == "" {
		return ""
	}
	return redacted
}

// redactURL hides the password of a connection URL. A URL that does not parse
// is reported as unparseable rather than printed, because it may still hold a
// password.
func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[UNPARSEABLE]"
	}
	return u.Redacted()
}
