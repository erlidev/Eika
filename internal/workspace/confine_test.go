package workspace

import (
	"slices"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
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

func TestEnvironmentPointsAProxiedWorkspaceAtTheProxy(t *testing.T) {
	h := &Host{opts: Options{EgressProxyURL: "http://eika:3128", HubURL: "http://eika:8080"}}
	ws := Workspace{ID: "ws1", HubToken: "secret", Proxied: true}
	env := h.environment(ws)
	for _, want := range []string{
		"HTTPS_PROXY=http://ws1:secret@eika:3128",
		"http_proxy=http://ws1:secret@eika:3128",
		"NO_PROXY=localhost,127.0.0.1,::1,eika",
		"NODE_USE_ENV_PROXY=1",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("environment %v lacks %s", env, want)
		}
	}
	ws.Proxied = false
	if env := h.environment(ws); env != nil {
		t.Errorf("an open workspace's environment = %v, want none", env)
	}
}

func TestUsageOfReadsASampleAsDockerStatsDoes(t *testing.T) {
	var s container.StatsResponse
	s.Read = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	s.CPUStats.CPUUsage.TotalUsage = 3_000_000
	s.PreCPUStats.CPUUsage.TotalUsage = 1_000_000
	s.CPUStats.SystemUsage = 20_000_000
	s.PreCPUStats.SystemUsage = 10_000_000
	s.CPUStats.OnlineCPUs = 4
	s.MemoryStats.Usage = 300 << 20
	s.MemoryStats.Limit = 1 << 30
	s.MemoryStats.Stats = map[string]uint64{"inactive_file": 100 << 20}
	s.PidsStats.Current = 12
	s.Networks = map[string]container.NetworkStats{"a": {RxBytes: 10, TxBytes: 5}, "b": {RxBytes: 1, TxBytes: 2}}

	u := usageOf(s)
	// A fifth of the host's time on four cores is 80% of one core.
	if u.CPUPercent != 80 {
		t.Errorf("cpu = %v%%, want 80%%", u.CPUPercent)
	}
	if u.MemoryBytes != 200<<20 || u.MemoryLimitBytes != 1<<30 {
		t.Errorf("memory = %d of %d, want 200 MiB of 1 GiB", u.MemoryBytes, u.MemoryLimitBytes)
	}
	if u.PIDs != 12 || u.NetworkRxBytes != 11 || u.NetworkTxBytes != 7 || !u.SampledAt.Equal(s.Read) {
		t.Errorf("usage = %+v", u)
	}
	// The first sample of a container has nothing to measure a rate against.
	s.PreCPUStats = container.CPUStats{}
	s.PreCPUStats.CPUUsage.TotalUsage = 5_000_000
	if u := usageOf(s); u.CPUPercent != 0 {
		t.Errorf("cpu with no earlier sample = %v, want 0", u.CPUPercent)
	}
}
