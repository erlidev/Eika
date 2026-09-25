package config_test

import (
	"errors"
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
		"EIKA_EIKAD_BINARY", "EIKA_HUB_ROOT", "EIKA_HUB_URL", "EIKA_AUTH_TOKEN",
		"EIKA_SECRET_KEY_FILE", "EIKA_ALLOWED_ORIGINS", "EIKA_PUBLIC_URL",
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

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(cfg, valid()) {
		t.Errorf("got %v, want the defaults", cfg)
	}
}

func TestLoadEmptyFileUsesDefaults(t *testing.T) {
	clearEnv(t)
	for name, body := range map[string]string{
		"empty":        "",
		"comment only": "# nothing set here\n",
		"whitespace":   "\n\n  \n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Load(writeConfig(t, body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !reflect.DeepEqual(cfg, valid()) {
				t.Errorf("got %v, want the defaults", cfg)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
listen: "127.0.0.1:9000"
database_url: "postgres://u:p@db:5432/eika"
docker_socket: "/run/docker.sock"
searxng_url: "http://search:8080"
sandbox_image: "custom:1"
secret_key_file: "/data/key"
auth_token: "s3cret"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9000" {
		t.Errorf("listen = %q, want 127.0.0.1:9000", cfg.Listen)
	}
	if cfg.AuthToken != "s3cret" {
		t.Errorf("auth_token = %q, want s3cret", cfg.AuthToken)
	}
	if cfg.SecretKeyFile != "/data/key" {
		t.Errorf("secret_key_file = %q, want /data/key", cfg.SecretKeyFile)
	}
}

func TestLoadPointsSettingsThatMovedToTheUI(t *testing.T) {
	clearEnv(t)
	for key, body := range map[string]string{
		"models":    "models:\n  - name: gpt-5\n",
		"subagents": "subagents:\n  max_depth: 3\n",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, body))
			if err == nil || !strings.Contains(err.Error(), "web UI") {
				t.Errorf("load = %v, want an error pointing at the web UI", err)
			}
		})
	}
}

func TestLoadFileErrors(t *testing.T) {
	clearEnv(t)
	t.Run("missing file", func(t *testing.T) {
		_, err := config.Load(filepath.Join(t.TempDir(), "absent.yaml"))
		if !errors.Is(err, config.ErrNoConfigFile) {
			t.Errorf("err = %v, want ErrNoConfigFile", err)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, "listne: \":1\"\n"))
		if err == nil {
			t.Fatal("load accepted an unknown field")
		}
	})
	t.Run("malformed yaml", func(t *testing.T) {
		_, err := config.Load(writeConfig(t, "listen: [\n"))
		if err == nil {
			t.Fatal("load accepted malformed yaml")
		}
	})
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
	t.Setenv("EIKA_EIKAD_BINARY", "/tmp/eikad")
	t.Setenv("EIKA_HUB_ROOT", "/tmp/hub")
	t.Setenv("EIKA_HUB_URL", "http://env:9090")
	t.Setenv("EIKA_SECRET_KEY_FILE", "/tmp/key")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := config.Config{
		Listen:         ":9999",
		DatabaseURL:    "postgres://x@db/eika",
		DockerSocket:   "/tmp/docker.sock",
		SearxNGURL:     "http://env:8080",
		SandboxImage:   "env-image:2",
		SandboxNetwork: "env-net",
		EikadBinary:    "/tmp/eikad",
		HubRoot:        "/tmp/hub",
		HubURL:         "http://env:9090",
		SecretKeyFile:  "/tmp/key",
		AuthToken:      "from-env",
		AllowedOrigins: []string{"localhost:9999", "127.0.0.1:9999"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %v, want %v", cfg, want)
	}
}

func TestValidate(t *testing.T) {
	missing := func(clear func(c *config.Config)) config.Config {
		c := valid()
		clear(&c)
		return c
	}

	cases := []struct {
		name string
		cfg  config.Config
		ok   bool
	}{
		{"defaults", valid(), true},
		{"zero value", config.Config{}, false},
		{"empty listen", missing(func(c *config.Config) { c.Listen = "" }), false},
		{"empty database url", missing(func(c *config.Config) { c.DatabaseURL = "" }), false},
		{"empty docker socket", missing(func(c *config.Config) { c.DockerSocket = "" }), false},
		{"empty searxng url", missing(func(c *config.Config) { c.SearxNGURL = "" }), false},
		{"empty sandbox image", missing(func(c *config.Config) { c.SandboxImage = "" }), false},
		{"empty sandbox network", missing(func(c *config.Config) { c.SandboxNetwork = "" }), true},
		{"empty eikad binary", missing(func(c *config.Config) { c.EikadBinary = "" }), false},
		{"empty hub root", missing(func(c *config.Config) { c.HubRoot = "" }), false},
		{"empty hub url", missing(func(c *config.Config) { c.HubURL = "" }), false},
		{"empty secret key file", missing(func(c *config.Config) { c.SecretKeyFile = "" }), false},
		{"no auth token, because sign-in sessions need none", missing(func(c *config.Config) { c.AuthToken = "" }), true},
		{"https public url", missing(func(c *config.Config) { c.PublicURL = "https://eika.example.com" }), true},
		{"http public url with a trailing slash", missing(func(c *config.Config) { c.PublicURL = "http://10.0.0.5:8080/" }), true},
		{"public url with a path", missing(func(c *config.Config) { c.PublicURL = "https://example.com/eika" }), false},
		{"public url without a scheme", missing(func(c *config.Config) { c.PublicURL = "eika.example.com" }), false},
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
	for _, want := range []string{"eika-sandbox:latest", "/var/lib/eika/secret.key", "postgres://eika:"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() dropped %q: %s", want, s)
		}
	}
}

func TestStringKeepsEmptyTokenEmpty(t *testing.T) {
	if got := config.Default().String(); !strings.Contains(got, "auth_token= ") {
		t.Errorf("String() = %s, want an empty auth_token", got)
	}
}

func TestStringRedactsAwkwardDatabasePasswords(t *testing.T) {
	cases := map[string]string{
		"at sign in password": "postgres://eika:hun@ter2@postgres:5432/eika",
		"escaped at sign":     "postgres://eika:hun%40ter2@postgres:5432/eika",
		"colon in password":   "postgres://eika:a:b:c@postgres:5432/eika",
		"password only":       "postgres://:hunter2@postgres:5432/eika",
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
			name:   "a listen address with a host gives that host",
			listen: "127.0.0.1:9000",
			want:   []string{"127.0.0.1:9000"},
		},
		{
			name:   "a configured origin is added to the harness's own",
			listen: ":8080",
			file:   "allowed_origins:\n  - \"ui.example\"\n",
			want:   []string{"localhost:8080", "127.0.0.1:8080", "ui.example"},
		},
		{
			name:   "a whole URL is reduced to its host",
			listen: ":8080",
			file:   "allowed_origins:\n  - \"https://ui.example:4443/app\"\n",
			want:   []string{"localhost:8080", "127.0.0.1:8080", "ui.example:4443"},
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
