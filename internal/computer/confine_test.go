package computer

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
)

func TestEnvironmentPointsAProxiedComputerAtTheProxy(t *testing.T) {
	h := &Host{opts: Options{EgressProxyURL: "http://eika:3128"}}
	cx := Computer{ID: "cx1", ProxyToken: "secret", Proxied: true}
	env := h.environment(cx)
	for _, want := range []string{
		"HTTPS_PROXY=http://cx1:secret@eika:3128",
		"http_proxy=http://cx1:secret@eika:3128",
		"NO_PROXY=localhost,127.0.0.1,::1",
		"NODE_USE_ENV_PROXY=1",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("environment %v lacks %s", env, want)
		}
	}
	cx.Proxied = false
	if env := h.environment(cx); env != nil {
		t.Errorf("an open computer's environment = %v, want none", env)
	}
}

func TestPlantedEntriesReachTheComputersCommands(t *testing.T) {
	d, err := eikad.New(eikad.Options{Root: t.TempDir(), Token: "tok"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	h := &Host{opts: Options{Plant: func(context.Context, string) []string { return []string{"OPENAI_API_KEY=sk-proj-fake"} }}}
	cx := Computer{ID: "cx1", Address: srv.URL, Token: "tok"}
	if err := h.setEnvironment(t.Context(), cx); err != nil {
		t.Fatal(err)
	}
	c, err := sandbox.New(sandbox.Options{BaseURL: srv.URL, Token: "tok", Root: d.Root()})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := c.Exec(t.Context(), executor.ExecSpec{Command: `printf %s "$OPENAI_API_KEY"`, Shell: true, Stdout: &out}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "sk-proj-fake" {
		t.Errorf("OPENAI_API_KEY = %q, want the planted value", out.String())
	}
}
