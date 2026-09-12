package config

import (
	"bytes"
	"errors"
	"fmt"
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
	// AuthToken is the bearer token required by every API route but /healthz.
	AuthToken string `yaml:"auth_token"`
	// Models lists the models the harness may use, in preference order.
	Models []Model `yaml:"models"`
}

// Model describes one OpenAI-compatible model endpoint.
type Model struct {
	// Name is the model identifier sent to the provider.
	Name string `yaml:"name"`
	// BaseURL is the OpenAI-compatible API root.
	BaseURL string `yaml:"base_url"`
	// APIKeyEnv names the environment variable holding the API key. The key
	// itself never appears in a configuration file.
	APIKeyEnv string `yaml:"api_key_env"`
	// ContextWindow is the model's total token budget.
	ContextWindow int `yaml:"context_window"`
	// MaxOutput is the maximum number of tokens the model may generate.
	MaxOutput int `yaml:"max_output"`
}

// ErrNoConfigFile reports that a named configuration file does not exist.
var ErrNoConfigFile = errors.New("config file not found")

// Default returns the configuration used when nothing else is set.
func Default() Config {
	return Config{
		Listen:       ":8080",
		DatabaseURL:  "postgres://eika:eika@postgres:5432/eika?sslmode=disable",
		DockerSocket: "/var/run/docker.sock",
		SearxNGURL:   "http://searxng:8080",
		SandboxImage: "eika-sandbox:latest",
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
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyEnv overlays the EIKA_* environment variables onto c. Models are
// configured in the file only, because a list does not fit an environment
// variable cleanly.
func (c *Config) applyEnv() {
	overrides := map[string]*string{
		"EIKA_LISTEN":        &c.Listen,
		"EIKA_DATABASE_URL":  &c.DatabaseURL,
		"EIKA_DOCKER_SOCKET": &c.DockerSocket,
		"EIKA_SEARXNG_URL":   &c.SearxNGURL,
		"EIKA_SANDBOX_IMAGE": &c.SandboxImage,
		"EIKA_AUTH_TOKEN":    &c.AuthToken,
	}
	for name, field := range overrides {
		if v, ok := os.LookupEnv(name); ok {
			*field = v
		}
	}
}

// Validate reports whether the configuration is usable.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("validate config: listen is empty")
	}
	if c.DatabaseURL == "" {
		return errors.New("validate config: database_url is empty")
	}
	if c.SandboxImage == "" {
		return errors.New("validate config: sandbox_image is empty")
	}
	seen := make(map[string]bool, len(c.Models))
	for i, m := range c.Models {
		switch {
		case m.Name == "":
			return fmt.Errorf("validate config: models[%d] has no name", i)
		case seen[m.Name]:
			return fmt.Errorf("validate config: model %s is declared twice", m.Name)
		case m.BaseURL == "":
			return fmt.Errorf("validate config: model %s has no base_url", m.Name)
		case m.APIKeyEnv == "":
			return fmt.Errorf("validate config: model %s has no api_key_env", m.Name)
		case m.ContextWindow <= 0:
			return fmt.Errorf("validate config: model %s has a non-positive context_window", m.Name)
		case m.MaxOutput <= 0:
			return fmt.Errorf("validate config: model %s has a non-positive max_output", m.Name)
		}
		seen[m.Name] = true
	}
	return nil
}

// Model returns the configured model with the given name.
func (c Config) Model(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// redacted is the placeholder that replaces a secret in String output.
const redacted = "[REDACTED]"

// String renders the configuration with secrets replaced by a placeholder so
// that it is safe to log.
func (c Config) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config{listen=%s database_url=%s docker_socket=%s searxng_url=%s sandbox_image=%s auth_token=%s models=[",
		c.Listen, redactURL(c.DatabaseURL), c.DockerSocket, c.SearxNGURL, c.SandboxImage, redact(c.AuthToken))
	for i, m := range c.Models {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s@%s(api_key_env=%s context_window=%d max_output=%d)",
			m.Name, m.BaseURL, m.APIKeyEnv, m.ContextWindow, m.MaxOutput)
	}
	b.WriteString("]}")
	return b.String()
}

// redact hides a secret while preserving whether it was set at all.
func redact(secret string) string {
	if secret == "" {
		return ""
	}
	return redacted
}

// redactURL hides the password of a scheme://user:password@host URL.
func redactURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}
	creds, host, ok := strings.Cut(rest, "@")
	if !ok {
		return raw
	}
	user, _, ok := strings.Cut(creds, ":")
	if !ok {
		return raw
	}
	return scheme + "://" + user + ":" + redacted + "@" + host
}
