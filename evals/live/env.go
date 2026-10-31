package live

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The variables of the bench. A missing optional one means that role of the
// bench is not running; EndpointVar is the one the suite cannot run without.
const (
	// EndpointVar names the chat model's base URL, without /v1.
	EndpointVar = "EIKA_LIVE_ENDPOINT"
	// KeyVar names the bearer key of the chat model.
	KeyVar = "EIKA_LIVE_KEY"
	// GuardVar names the injection screen's URL.
	GuardVar = "EIKA_GUARD_URL"
	// DecisionVar names the decision model's URL.
	DecisionVar = "EIKA_DECISION_URL"
)

// Env holds the values of the bench's variables.
type Env map[string]string

// EnvFile returns the path of the bench's variable file under home.
func EnvFile(home string) string {
	return filepath.Join(home, ".config", "eika", "live.env")
}

// LoadEnv returns the bench's variables: every KEY=VALUE line of the file at
// path, except where lookup already has the variable, which wins, so a value
// exported in the shell overrides the file. A missing file is not an error:
// the variables may all come from the environment. The environment is read
// through lookup (os.LookupEnv in the tests that run) and never changed.
func LoadEnv(path string, lookup func(string) (string, bool)) (Env, error) {
	env := Env{}
	f, err := os.Open(path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, fmt.Errorf("open live env file: %w", err)
	default:
		defer f.Close()
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			key, value, ok, err := parseEnvLine(sc.Text())
			if err != nil {
				// The line is not quoted: it may hold the key.
				return nil, fmt.Errorf("parse %s line %d: %w", path, n, err)
			}
			if ok {
				env[key] = value
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("read live env file: %w", err)
		}
	}
	keys := []string{EndpointVar, KeyVar, GuardVar, DecisionVar}
	for key := range env {
		keys = append(keys, key)
	}
	for _, key := range keys {
		if v, set := lookup(key); set {
			env[key] = v
		}
	}
	return env, nil
}

// parseEnvLine reads one line of an env file. ok is false for a blank line
// or a comment. A value may be wrapped in one pair of quotes.
func parseEnvLine(line string) (key, value string, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, value, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" {
		return "", "", false, fmt.Errorf("not KEY=VALUE")
	}
	value = strings.TrimSpace(value)
	if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
		value = value[1 : n-1]
	}
	return key, value, true, nil
}
