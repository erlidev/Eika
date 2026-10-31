package computer

import (
	"slices"
	"testing"
)

func TestCreateResourcesSetsOnlyTheLimitsGiven(t *testing.T) {
	none := createResources(Limits{PIDs: -1})
	if none.NanoCPUs != 0 || none.Memory != 0 || none.MemorySwap != 0 || none.PidsLimit != nil {
		t.Errorf("no limits = %+v, want none set", none)
	}
	all := createResources(Limits{CPUs: 1.5, MemoryBytes: 1 << 30, PIDs: 512})
	if all.NanoCPUs != 1_500_000_000 || all.Memory != 1<<30 || all.PidsLimit == nil || *all.PidsLimit != 512 {
		t.Errorf("limits = %+v, want 1.5 cores, 1 GiB, and 512 processes", all)
	}
	// No swap beyond the memory limit, so the limit is what the processes
	// can hold.
	if all.MemorySwap != all.Memory {
		t.Errorf("memory+swap = %d, want the memory limit %d", all.MemorySwap, all.Memory)
	}
}

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
