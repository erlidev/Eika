package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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

// Usage is what a running workspace is consuming, sampled once.
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
// which bounds any limit a workspace can be given.
func (h *Host) Capacity(ctx context.Context) (Capacity, error) {
	info, err := h.docker.Info(ctx)
	if err != nil {
		return Capacity{}, fmt.Errorf("read docker host capacity: %w", err)
	}
	return Capacity{CPUs: info.NCPU, MemoryBytes: info.MemTotal}, nil
}

// Confine applies a confinement to an existing workspace, running or
// stopped, without restarting it. A running workspace's processes are held
// to the new limits at once; a change of network drops the connections they
// have open, and the processes it starts from then on get the proxy
// settings that go with the new network.
func (h *Host) Confine(ctx context.Context, ws *Workspace, c Confinement) error {
	if c.Proxied && !h.EgressControl() {
		return ErrNoEgressControl
	}
	resources, err := h.updateResources(ctx, c.Limits)
	if err != nil {
		return err
	}
	if _, err := h.docker.ContainerUpdate(ctx, ws.ContainerID, container.UpdateConfig{Resources: resources}); err != nil {
		return fmt.Errorf("update limits of workspace %s: %w", ws.ID, err)
	}
	if h.opts.Network != "" {
		if err := h.moveTo(ctx, *ws, h.networkFor(c.Proxied)); err != nil {
			return err
		}
	}
	ws.Proxied = c.Proxied
	if ws.State == StateRunning {
		if err := h.setEnvironment(ctx, *ws); err != nil {
			return err
		}
	}
	h.log.Info("workspace confined", "workspace_id", ws.ID, "cpus", c.Limits.CPUs,
		"memory_bytes", c.Limits.MemoryBytes, "pids", c.Limits.PIDs, "proxied", c.Proxied)
	return nil
}

// Usage samples what a running workspace is consuming. It takes about a
// second: the daemon reads the CPU counters twice to measure a rate.
func (h *Host) Usage(ctx context.Context, ws Workspace) (Usage, error) {
	resp, err := h.docker.ContainerStats(ctx, ws.ContainerID, false)
	if err != nil {
		return Usage{}, fmt.Errorf("read usage of workspace %s: %w", ws.ID, err)
	}
	defer resp.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return Usage{}, fmt.Errorf("decode usage of workspace %s: %w", ws.ID, err)
	}
	return usageOf(stats), nil
}

// PortURL is the base URL the harness reaches a port of a workspace on: the
// container name on the sandbox network, or the container's own address
// when the harness runs outside that network.
func (h *Host) PortURL(ctx context.Context, ws Workspace, port int) (string, error) {
	if h.opts.Network != "" {
		return "http://" + ContainerName(ws.ID) + ":" + strconv.Itoa(port), nil
	}
	info, err := h.docker.ContainerInspect(ctx, ws.ContainerID)
	if err != nil {
		return "", fmt.Errorf("inspect workspace %s: %w", ws.ID, err)
	}
	if info.NetworkSettings != nil {
		for _, n := range info.NetworkSettings.Networks {
			if n != nil && n.IPAddress != "" {
				return "http://" + n.IPAddress + ":" + strconv.Itoa(port), nil
			}
		}
	}
	return "", fmt.Errorf("workspace %s has no address on any network", ws.ID)
}

// networkFor is the sandbox network a workspace joins.
func (h *Host) networkFor(proxied bool) string {
	if proxied {
		return h.opts.InternalNetwork
	}
	return h.opts.Network
}

// moveTo attaches a workspace's container to one sandbox network and
// detaches it from the other. It connects first, so the harness can reach
// the daemon by name throughout, and does nothing that is already so.
func (h *Host) moveTo(ctx context.Context, ws Workspace, target string) error {
	info, err := h.docker.ContainerInspect(ctx, ws.ContainerID)
	if err != nil {
		return fmt.Errorf("inspect workspace %s: %w", ws.ID, err)
	}
	attached := map[string]*network.EndpointSettings{}
	if info.NetworkSettings != nil {
		attached = info.NetworkSettings.Networks
	}
	if _, ok := attached[target]; !ok {
		if err := h.docker.NetworkConnect(ctx, target, ws.ContainerID, &network.EndpointSettings{
			Aliases: []string{ContainerName(ws.ID)},
		}); err != nil {
			return fmt.Errorf("connect workspace %s to %s: %w", ws.ID, target, err)
		}
	}
	for _, other := range []string{h.opts.Network, h.opts.InternalNetwork} {
		if other == "" || other == target {
			continue
		}
		if _, ok := attached[other]; !ok {
			continue
		}
		if err := h.docker.NetworkDisconnect(ctx, other, ws.ContainerID, true); err != nil {
			return fmt.Errorf("disconnect workspace %s from %s: %w", ws.ID, other, err)
		}
	}
	return nil
}

// setEnvironment gives a running workspace's daemon the environment its
// processes need: the egress proxy for a proxied workspace, nothing for one
// with a route of its own.
func (h *Host) setEnvironment(ctx context.Context, ws Workspace) error {
	c, err := sandbox.New(sandbox.Options{BaseURL: ws.Address, Token: ws.Token, Root: Root})
	if err != nil {
		return err
	}
	if err := c.SetEnvironment(ctx, h.environment(ws)); err != nil {
		return fmt.Errorf("set the environment of workspace %s: %w", ws.ID, err)
	}
	return nil
}

// environment is what a workspace's processes inherit from the daemon. A
// proxied workspace reaches the internet through the egress proxy, which
// knows it by its id and hub token; the hub and loopback are reached
// directly. Both spellings are set because tools disagree on which they
// read, and NODE_USE_ENV_PROXY makes Node's own fetch read them at all.
func (h *Host) environment(ws Workspace) []string {
	if !ws.Proxied || h.opts.EgressProxyURL == "" {
		return nil
	}
	proxy, err := url.Parse(h.opts.EgressProxyURL)
	if err != nil {
		return nil
	}
	proxy.User = url.UserPassword(ws.ID, ws.HubToken)
	direct := []string{"localhost", "127.0.0.1", "::1"}
	if hub, err := url.Parse(h.opts.HubURL); err == nil && hub.Hostname() != "" {
		direct = append(direct, hub.Hostname())
	}
	noProxy := strings.Join(direct, ",")
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
