package eikad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
)

// maxEnvironmentBytes caps the body of PUT /environment.
const maxEnvironmentBytes = 64 << 10

// handleSetEnvironment replaces the entries the daemon adds to the
// environment of every process it starts: commands, terminals, and stdio
// processes started after the request. It is how the harness routes a
// sandbox's traffic through its egress proxy, and stops doing so, without
// restarting the container. A process already running keeps what it had.
func (d *Daemon) handleSetEnvironment(w http.ResponseWriter, r *http.Request) {
	var req EnvironmentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnvironmentBytes)).Decode(&req); err != nil {
		writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("decode environment request: %w", err))
		return
	}
	for _, kv := range req.Env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok || key == "" || strings.ContainsRune(key, 0) {
			writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("environment entry %q is not KEY=VALUE", kv))
			return
		}
		if key == TokenEnv {
			writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("environment entry %s is the daemon's own", key))
			return
		}
	}
	d.mu.Lock()
	d.env = slices.Clone(req.Env)
	d.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// environ is the environment a process the daemon starts inherits: the
// daemon's own without its token, then what the harness set.
func (d *Daemon) environ() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append(sanitizedEnv(), d.env...)
}

// sanitizedEnv is the daemon's environment without the daemon's own token.
func sanitizedEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, TokenEnv+"=") {
			out = append(out, kv)
		}
	}
	return out
}
