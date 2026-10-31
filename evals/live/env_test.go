package live_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

func lookupIn(set map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := set[key]
		return v, ok
	}
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "live.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEnvReadsTheFile(t *testing.T) {
	path := writeEnvFile(t, `# the bench
EIKA_LIVE_KEY=secret-from-file

export EIKA_LIVE_ENDPOINT="http://127.0.0.1:8091"
EIKA_GUARD_URL = 'http://127.0.0.1:9940'
`)
	env, err := live.LoadEnv(path, lookupIn(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		live.KeyVar:      "secret-from-file",
		live.EndpointVar: "http://127.0.0.1:8091",
		live.GuardVar:    "http://127.0.0.1:9940",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
}

func TestLoadEnvDoesNotOverrideTheEnvironment(t *testing.T) {
	path := writeEnvFile(t, "EIKA_LIVE_ENDPOINT=http://from-file\nEIKA_LIVE_KEY=file-key\n")
	env, err := live.LoadEnv(path, lookupIn(map[string]string{
		live.EndpointVar: "http://from-shell",
		live.DecisionVar: "http://decision",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if env[live.EndpointVar] != "http://from-shell" {
		t.Errorf("endpoint = %q, want the shell's", env[live.EndpointVar])
	}
	if env[live.KeyVar] != "file-key" {
		t.Errorf("key = %q, want the file's, since the shell has none", env[live.KeyVar])
	}
	if env[live.DecisionVar] != "http://decision" {
		t.Errorf("a variable only the shell sets is dropped: %q", env[live.DecisionVar])
	}
}

func TestLoadEnvWithoutAFileUsesTheEnvironment(t *testing.T) {
	env, err := live.LoadEnv(filepath.Join(t.TempDir(), "missing.env"), lookupIn(map[string]string{live.EndpointVar: "http://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if env[live.EndpointVar] != "http://x" {
		t.Errorf("endpoint = %q", env[live.EndpointVar])
	}
}

func TestLoadEnvRejectsAMalformedLineWithoutQuotingIt(t *testing.T) {
	path := writeEnvFile(t, "EIKA_LIVE_ENDPOINT=http://x\nsk-this-looks-like-a-key\n")
	_, err := live.LoadEnv(path, lookupIn(nil))
	if err == nil {
		t.Fatal("a line without = was accepted")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not say which line", err)
	}
	if strings.Contains(err.Error(), "sk-this") {
		t.Errorf("error %q quotes the line, which may hold a key", err)
	}
}
