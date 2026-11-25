package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/config"
)

// writeConfig writes body to a temporary file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eika.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// clearEnv removes every EIKA_* override for the duration of the test so that
// the developer's own environment cannot change the result.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"EIKA_LISTEN", "EIKA_DATABASE_URL", "EIKA_DOCKER_SOCKET",
		"EIKA_SEARXNG_URL", "EIKA_SANDBOX_IMAGE", "EIKA_SANDBOX_NETWORK",
		"EIKA_EIKAD_BINARY", "EIKA_AUTH_TOKEN",
		"EIKA_SECRET_KEY_FILE", "EIKA_ALLOWED_ORIGINS", "EIKA_PUBLIC_URL", "EIKA_GUARD_URL",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// valid returns the defaults plus what Load adds to them: the origins derived
// from the listen address.
func valid() config.Config {
	c := config.Default()
	c.AllowedOrigins = []string{"localhost:8080", "127.0.0.1:8080"}
	return c
}

func TestLoadEnvOverridesFile(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, "listen: \":1\"\nauth_token: \"from-file\"\n")
	t.Setenv("EIKA_LISTEN", ":9999")
	t.Setenv("EIKA_AUTH_TOKEN", "from-env")
	t.Setenv("EIKA_DATABASE_URL", "postgres://x@db/eika")
	t.Setenv("EIKA_DOCKER_SOCKET", "/tmp/docker.sock")
	t.Setenv("EIKA_SEARXNG_URL", "http://env:8080")
	t.Setenv("EIKA_SANDBOX_IMAGE", "env-image:2")
	t.Setenv("EIKA_SANDBOX_NETWORK", "env-net")
	t.Setenv("EIKA_SANDBOX_INTERNAL_NETWORK", "env-internal")
	t.Setenv("EIKA_EGRESS_LISTEN", ":4000")
	t.Setenv("EIKA_EGRESS_PROXY_URL", "http://env:4000")
	t.Setenv("EIKA_EIKAD_BINARY", "/tmp/eikad")
	t.Setenv("EIKA_SECRET_KEY_FILE", "/tmp/key")
	t.Setenv("EIKA_GUARD_URL", "http://gpu:9940")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := config.Config{
		Listen:                 ":9999",
		DatabaseURL:            "postgres://x@db/eika",
		DockerSocket:           "/tmp/docker.sock",
		SearxNGURL:             "http://env:8080",
		SandboxImage:           "env-image:2",
		SandboxNetwork:         "env-net",
		SandboxInternalNetwork: "env-internal",
		EgressListen:           ":4000",
		EgressProxyURL:         "http://env:4000",
		EikadBinary:            "/tmp/eikad",
		SecretKeyFile:          "/tmp/key",
		AuthToken:              "from-env",
		AllowedOrigins:         []string{"localhost:9999", "127.0.0.1:9999"},
		GuardURL:               "http://gpu:9940",
		ScreenThreshold:        0.9,
		MemoryScreenThreshold:  0.99,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %v, want %v", cfg, want)
	}
}

func TestValidate(t *testing.T) {
	missing := func(unset func(c *config.Config)) config.Config {
		c := valid()
		unset(&c)
		return c
	}

	cases := []struct {
		name string
		cfg  config.Config
		ok   bool
	}{
		{"empty database url", missing(func(c *config.Config) { c.DatabaseURL = "" }), false},
		{"no internal network, so no restricted egress", missing(func(c *config.Config) { c.SandboxInternalNetwork = "" }), true},
		{"one network for both kinds of sandbox", missing(func(c *config.Config) { c.SandboxInternalNetwork = c.SandboxNetwork }), false},
		{"an egress proxy url without a scheme", missing(func(c *config.Config) { c.EgressProxyURL = "eika:3128" }), false},
		{"no egress proxy in development, where nothing is restricted", missing(func(c *config.Config) { c.SandboxNetwork = ""; c.EgressListen = "" }), true},
		{"no auth token, because sign-in sessions need none", missing(func(c *config.Config) { c.AuthToken = "" }), true},
		{"https public url", missing(func(c *config.Config) { c.PublicURL = "https://eika.example.com" }), true},
		{"public url with a path", missing(func(c *config.Config) { c.PublicURL = "https://example.com/eika" }), false},
		{"public url with credentials", missing(func(c *config.Config) { c.PublicURL = "https://u:p@eika.example.com" }), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if c.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !c.ok && err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

func TestStringRedactsSecrets(t *testing.T) {
	cfg := valid()
	cfg.AuthToken = "top-secret-token"
	cfg.DatabaseURL = "postgres://eika:hunter2@postgres:5432/eika"

	s := cfg.String()
	for _, secret := range []string{"top-secret-token", "hunter2"} {
		if strings.Contains(s, secret) {
			t.Errorf("String() leaked %q: %s", secret, s)
		}
	}
	if !strings.Contains(s, "postgres://eika:") {
		t.Errorf("String() dropped the rest of the database url: %s", s)
	}
}

func TestStringRedactsAwkwardDatabasePasswords(t *testing.T) {
	cases := map[string]string{
		"at sign in password": "postgres://eika:hun@ter2@postgres:5432/eika",
		"colon in password":   "postgres://eika:a:b:c@postgres:5432/eika",
		"unparseable":         "://%%not a url",
	}
	for name, dsn := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			cfg.DatabaseURL = dsn
			got := cfg.String()
			for _, secret := range []string{"hunter2", "hun@ter2", "hun%40ter2", "a:b:c", "not a url"} {
				if strings.Contains(got, secret) {
					t.Errorf("String() leaked %q from %q: %s", secret, dsn, got)
				}
			}
		})
	}
}

func TestAllowedOrigins(t *testing.T) {
	cases := []struct {
		name   string
		listen string
		file   string
		env    string
		want   []string
	}{
		{
			name:   "a listen address with no host is reachable under either loopback name",
			listen: ":8080",
			want:   []string{"localhost:8080", "127.0.0.1:8080"},
		},
		{
			name:   "a configured origin is added to the harness's own",
			listen: ":8080",
			file:   "allowed_origins:\n  - \"ui.example\"\n",
			want:   []string{"localhost:8080", "127.0.0.1:8080", "ui.example"},
		},
		{
			name:   "the environment replaces the file's list",
			listen: ":8080",
			file:   "allowed_origins:\n  - \"from-file.example\"\n",
			env:    "http://localhost:5173, from-env.example ",
			want:   []string{"localhost:8080", "127.0.0.1:8080", "localhost:5173", "from-env.example"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("EIKA_LISTEN", c.listen)
			if c.env != "" {
				t.Setenv("EIKA_ALLOWED_ORIGINS", c.env)
			}
			cfg, err := config.Load(writeConfig(t, c.file))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !reflect.DeepEqual(cfg.AllowedOrigins, c.want) {
				t.Errorf("allowed_origins = %v, want %v", cfg.AllowedOrigins, c.want)
			}
		})
	}
}

func TestFetchFixtureHosts(t *testing.T) {
	clearEnv(t)
	t.Setenv("EIKA_FETCH_FIXTURE_HOSTS", "fixture, other.local")
	cfg, err := config.Load("")
	if err != nil || len(cfg.FetchFixtureHosts) != 2 || cfg.FetchFixtureHosts[0] != "fixture" {
		t.Fatalf("fixture hosts: %v, %v", cfg.FetchFixtureHosts, err)
	}
	for _, invalid := range []string{"*.example.com", "http://fixture", "127.0.0.1", "fixture:80"} {
		cfg.FetchFixtureHosts = []string{invalid}
		if cfg.Validate() == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}
