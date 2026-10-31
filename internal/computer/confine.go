package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/erlidev/eika/internal/executor/sandbox"
)

// ErrNoEgressControl reports a proxied confinement on a host that cannot
// give one: it has no internal sandbox network, which is the case for a
// harness running outside the compose stack.
var ErrNoEgressControl = errors.New("restricting a sandbox's network needs the internal sandbox network, which this harness is not configured with")

// Capacity is what the Docker host has to give its containers.
type Capacity struct {
	// CPUs is the number of cores the daemon reports.
	CPUs int
	// MemoryBytes is the host's total memory.
	MemoryBytes int64
}

// Usage is what a running computer is consuming, sampled once.
type Usage struct {
	// CPUPercent is the CPU time the container used over the sample, where
	// 100 is one whole core.
	CPUPercent float64
	// MemoryBytes is the memory the container holds, without the page cache
	// the kernel can reclaim, which is how Docker counts it too.
	MemoryBytes int64
	// MemoryLimitBytes is what the container may hold: its limit, or the
	// host's memory when it has none.
	MemoryLimitBytes int64
	// PIDs is the number of processes and threads in the container.
	PIDs int64
	// NetworkRxBytes and NetworkTxBytes are the bytes the container received
	// and sent since it started, over every network it is on.
	NetworkRxBytes int64
	NetworkTxBytes int64
	// SampledAt is when the daemon read the numbers, in UTC.
	SampledAt time.Time
}

// EgressControl reports whether the host can proxy a sandbox: it runs its
// sandboxes on a network of their own and has an internal network beside it.
func (h *Host) EgressControl() bool {
	return h.opts.Network != "" && h.opts.InternalNetwork != ""
}

// Capacity reports how many cores and how much memory the Docker host has,
// which bounds any limit a computer can be given.
func (h *Host) Capacity(ctx context.Context) (Capacity, error) {
	info, err := h.docker.Info(ctx)
	if err != nil {
		return Capacity{}, fmt.Errorf("read docker host capacity: %w", err)
	}
	return Capacity{CPUs: info.NCPU, MemoryBytes: info.MemTotal}, nil
}

// Confine applies a confinement to an existing computer, running or
// stopped, without restarting it. A running computer's processes are held
// to the new limits at once; a change of network drops the connections they
// have open, and the processes it starts from then on get the proxy
// settings that go with the new network.
func (h *Host) Confine(ctx context.Context, cx *Computer, c Confinement) error {
	if c.Proxied && !h.EgressControl() {
		return ErrNoEgressControl
	}
	resources, err := h.updateResources(ctx, c.Limits)
	if err != nil {
		return err
	}
	if _, err := h.docker.ContainerUpdate(ctx, cx.ContainerID, container.UpdateConfig{Resources: resources}); err != nil {
		return fmt.Errorf("update limits of computer %s: %w", cx.ID, err)
	}
	if h.opts.Network != "" {
		if err := h.moveTo(ctx, *cx, h.networkFor(c.Proxied)); err != nil {
			return err
		}
	}
	cx.Proxied = c.Proxied
	if cx.State == StateRunning {
		if err := h.setEnvironment(ctx, *cx); err != nil {
			return err
		}
	}
	h.log.Info("computer confined", "computer_id", cx.ID, "cpus", c.Limits.CPUs,
		"memory_bytes", c.Limits.MemoryBytes, "pids", c.Limits.PIDs, "proxied", c.Proxied)
	return nil
}

// Usage samples what a running computer is consuming. It takes about a
// second: the daemon reads the CPU counters twice to measure a rate.
func (h *Host) Usage(ctx context.Context, cx Computer) (Usage, error) {
	resp, err := h.docker.ContainerStats(ctx, cx.ContainerID, false)
	if err != nil {
		return Usage{}, fmt.Errorf("read usage of computer %s: %w", cx.ID, err)
	}
	defer resp.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return Usage{}, fmt.Errorf("decode usage of computer %s: %w", cx.ID, err)
	}
	return usageOf(stats), nil
}

// networkFor is the sandbox network a computer joins.
func (h *Host) networkFor(proxied bool) string {
	if proxied {
		return h.opts.InternalNetwork
	}
	return h.opts.Network
}

// moveTo attaches a computer's container to one sandbox network and
// detaches it from the other. It connects first, so the harness can reach
// the daemon by name throughout, and does nothing that is already so.
//
// A container remembers a network by its id, so one created before its
// network was recreated, as `docker compose down` and `up` do, still names
// the old network and cannot start. Connecting again to the network of that
// name replaces the stale entry, and a forced disconnect removes one.
func (h *Host) moveTo(ctx context.Context, cx Computer, target string) error {
	info, err := h.docker.ContainerInspect(ctx, cx.ContainerID)
	if err != nil {
		return fmt.Errorf("inspect computer %s: %w", cx.ID, err)
	}
	attached := map[string]*network.EndpointSettings{}
	if info.NetworkSettings != nil {
		attached = info.NetworkSettings.Networks
	}
	current, err := h.docker.NetworkInspect(ctx, target, network.InspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect network %s: %w", target, err)
	}
	if ep, ok := attached[target]; !ok || ep == nil || ep.NetworkID != current.ID {
		if err := h.docker.NetworkConnect(ctx, target, cx.ContainerID, &network.EndpointSettings{
			Aliases: []string{ContainerName(cx.ID)},
		}); err != nil {
			return fmt.Errorf("connect computer %s to %s: %w", cx.ID, target, err)
		}
	}
	for _, other := range []string{h.opts.Network, h.opts.InternalNetwork} {
		if other == "" || other == target {
			continue
		}
		if _, ok := attached[other]; !ok {
			continue
		}
		if err := h.docker.NetworkDisconnect(ctx, other, cx.ContainerID, true); err != nil {
			return fmt.Errorf("disconnect computer %s from %s: %w", cx.ID, other, err)
		}
	}
	return nil
}

// setEnvironment gives a running computer's daemon the environment its
// processes need: the egress proxy for a proxied computer, nothing for one
// with a route of its own.
func (h *Host) setEnvironment(ctx context.Context, cx Computer) error {
	c, err := sandbox.New(sandbox.Options{BaseURL: cx.Address, Token: cx.Token, Root: Root})
	if err != nil {
		return err
	}
	if err := c.SetEnvironment(ctx, h.environment(cx)); err != nil {
		return fmt.Errorf("set the environment of computer %s: %w", cx.ID, err)
	}
	return nil
}

// environment is what a computer's processes inherit from the daemon. A
// proxied computer reaches the internet through the egress proxy, which
// knows it by its id and proxy token; loopback is reached
// directly. Both spellings are set because tools disagree on which they
// read, and NODE_USE_ENV_PROXY makes Node's own fetch read them at all.
func (h *Host) environment(cx Computer) []string {
	if !cx.Proxied || h.opts.EgressProxyURL == "" {
		return nil
	}
	proxy, err := url.Parse(h.opts.EgressProxyURL)
	if err != nil {
		return nil
	}
	proxy.User = url.UserPassword(cx.ID, cx.ProxyToken)
	noProxy := "localhost,127.0.0.1,::1"
	return []string{
		"HTTP_PROXY=" + proxy.String(),
		"HTTPS_PROXY=" + proxy.String(),
		"http_proxy=" + proxy.String(),
		"https_proxy=" + proxy.String(),
		"NO_PROXY=" + noProxy,
		"no_proxy=" + noProxy,
		"NODE_USE_ENV_PROXY=1",
	}
}

// createResources is the Docker form of a new container's limits, where a
// zero field sets no limit.
func createResources(l Limits) container.Resources {
	r := container.Resources{NanoCPUs: int64(l.CPUs * 1e9)}
	if l.MemoryBytes > 0 {
		r.Memory, r.MemorySwap = l.MemoryBytes, l.MemoryBytes
	}
	if l.PIDs > 0 {
		r.PidsLimit = &l.PIDs
	}
	return r
}

// updateResources is the Docker form of an existing container's limits.
// Docker reads a zero CPU or memory limit in an update as "unchanged", and
// refuses to lift either, so no limit is the whole host: every core, and all
// of its memory.
func (h *Host) updateResources(ctx context.Context, l Limits) (container.Resources, error) {
	r := createResources(l)
	unlimited := int64(-1)
	if l.PIDs <= 0 {
		r.PidsLimit = &unlimited
	}
	if l.CPUs > 0 && l.MemoryBytes > 0 {
		return r, nil
	}
	capacity, err := h.Capacity(ctx)
	if err != nil {
		return container.Resources{}, err
	}
	if l.CPUs <= 0 {
		r.NanoCPUs = int64(capacity.CPUs) * 1e9
	}
	if l.MemoryBytes <= 0 {
		r.Memory, r.MemorySwap = capacity.MemoryBytes, capacity.MemoryBytes
	}
	return r, nil
}

// usageOf reads a Docker stats sample the way `docker stats` does.
func usageOf(s container.StatsResponse) Usage {
	u := Usage{
		MemoryLimitBytes: int64(s.MemoryStats.Limit),
		PIDs:             int64(s.PidsStats.Current),
		SampledAt:        s.Read.UTC(),
	}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	cores := float64(s.CPUStats.OnlineCPUs)
	if cores == 0 {
		cores = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta > 0 && systemDelta > 0 {
		u.CPUPercent = cpuDelta / systemDelta * cores * 100
	}
	// cgroup v2 names the reclaimable cache inactive_file, v1
	// total_inactive_file.
	cache := s.MemoryStats.Stats["inactive_file"]
	if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok {
		cache = v
	}
	if s.MemoryStats.Usage > cache {
		u.MemoryBytes = int64(s.MemoryStats.Usage - cache)
	}
	for _, n := range s.Networks {
		u.NetworkRxBytes += int64(n.RxBytes)
		u.NetworkTxBytes += int64(n.TxBytes)
	}
	return u
}
