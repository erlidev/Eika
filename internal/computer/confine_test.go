package computer

import (
	"slices"
	"testing"
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
