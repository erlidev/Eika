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

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(cfg, config.Default()) {
		t.Errorf("got %v, want the defaults", cfg)
	}
}

func TestLoadFile(t *testing.T) {
	path := writeConfig(t, `
listen: "127.0.0.1:9000"
database_url: "postgres://u:p@db:5432/eika"
docker_socket: "/run/docker.sock"
searxng_url: "http://search:8080"
sandbox_image: "custom:1"
auth_token: "s3cret"
models:
  - name: gpt-5
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY
    context_window: 400000
    max_output: 128000
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
	m, ok := cfg.Model("gpt-5")
	if !ok {
		t.Fatal("model gpt-5 not found")
	}
	if m.ContextWindow != 400000 || m.MaxOutput != 128000 || m.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("model = %+v, want the file values", m)
	}
	if _, ok := cfg.Model("absent"); ok {
		t.Error("Model reported an undeclared model as present")
	}
}

func TestLoadFileErrors(t *testing.T) {
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
	path := writeConfig(t, "listen: \":1\"\nauth_token: \"from-file\"\n")
	t.Setenv("EIKA_LISTEN", ":9999")
	t.Setenv("EIKA_AUTH_TOKEN", "from-env")
	t.Setenv("EIKA_DATABASE_URL", "postgres://x@db/eika")
	t.Setenv("EIKA_DOCKER_SOCKET", "/tmp/docker.sock")
	t.Setenv("EIKA_SEARXNG_URL", "http://env:8080")
	t.Setenv("EIKA_SANDBOX_IMAGE", "env-image:2")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := config.Config{
		Listen:       ":9999",
		DatabaseURL:  "postgres://x@db/eika",
		DockerSocket: "/tmp/docker.sock",
		SearxNGURL:   "http://env:8080",
		SandboxImage: "env-image:2",
		AuthToken:    "from-env",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %v, want %v", cfg, want)
	}
}

func TestValidate(t *testing.T) {
	model := config.Model{
		Name:          "m",
		BaseURL:       "http://api",
		APIKeyEnv:     "KEY",
		ContextWindow: 1,
		MaxOutput:     1,
	}
	withModels := func(ms ...config.Model) config.Config {
		c := config.Default()
		c.Models = ms
		return c
	}
	noName := model
	noName.Name = ""
	noBase := model
	noBase.BaseURL = ""
	noKeyEnv := model
	noKeyEnv.APIKeyEnv = ""
	noWindow := model
	noWindow.ContextWindow = 0
	noOutput := model
	noOutput.MaxOutput = -1
	other := model
	other.Name = "other"

	cases := []struct {
		name string
		cfg  config.Config
		ok   bool
	}{
		{"defaults", config.Default(), true},
		{"valid models", withModels(model, other), true},
		{"empty listen", config.Config{}, false},
		{"model without name", withModels(noName), false},
		{"model without base url", withModels(noBase), false},
		{"model without api key env", withModels(noKeyEnv), false},
		{"model without context window", withModels(noWindow), false},
		{"model with negative max output", withModels(noOutput), false},
		{"duplicate model", withModels(model, model), false},
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
	cfg := config.Default()
	cfg.AuthToken = "top-secret-token"
	cfg.DatabaseURL = "postgres://eika:hunter2@postgres:5432/eika"
	cfg.Models = []config.Model{{
		Name:          "m",
		BaseURL:       "http://api",
		APIKeyEnv:     "OPENAI_API_KEY",
		ContextWindow: 8,
		MaxOutput:     4,
	}}

	s := cfg.String()
	for _, secret := range []string{"top-secret-token", "hunter2"} {
		if strings.Contains(s, secret) {
			t.Errorf("String() leaked %q: %s", secret, s)
		}
	}
	for _, want := range []string{"eika-sandbox:latest", "OPENAI_API_KEY", "postgres://eika:"} {
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
