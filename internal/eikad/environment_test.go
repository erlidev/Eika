package eikad_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/eikad"
)

// setEnvironment sends PUT /environment and returns the status.
func setEnvironment(t *testing.T, base, body string) int {
	t.Helper()
	resp := do(t, http.MethodPut, base+"/environment", strings.NewReader(body))
	return resp.StatusCode
}

func TestTheEnvironmentReachesEveryLaterCommand(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	if status := setEnvironment(t, base, `{"env":["HTTPS_PROXY=http://proxy:3128","GREETING=hei"]}`); status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: `printf '%s %s' "$HTTPS_PROXY" "$GREETING"`, Shell: true})
	if stdout != "http://proxy:3128 hei" {
		t.Errorf("stdout = %q, want the entries the harness set", stdout)
	}

	t.Run("a request's own entries win", func(t *testing.T) {
		stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: `printf %s "$GREETING"`, Shell: true, Env: []string{"GREETING=moi"}})
		if stdout != "moi" {
			t.Errorf("stdout = %q, want the request's entry", stdout)
		}
	})

	t.Run("an empty list clears them", func(t *testing.T) {
		if status := setEnvironment(t, base, `{"env":[]}`); status != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", status)
		}
		stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: `printf %s "${HTTPS_PROXY-unset}"`, Shell: true})
		if stdout != "unset" {
			t.Errorf("stdout = %q, want the entry gone", stdout)
		}
	})
}

func TestTheEnvironmentRefusesWhatIsNotAnEntry(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	for name, body := range map[string]string{
		"no equals sign":     `{"env":["PROXY"]}`,
		"empty key":          `{"env":["=value"]}`,
		"the daemon's token": `{"env":["` + eikad.TokenEnv + `=stolen"]}`,
		"not json":           `env`,
	} {
		t.Run(name, func(t *testing.T) {
			if status := setEnvironment(t, base, body); status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", status)
			}
		})
	}
}
