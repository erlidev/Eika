package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace"
)

// Bounds on a workspace's sandbox. The limits' upper bounds are the Docker
// host's own, read when a value is checked.
const (
	// minCPUs is the smallest share of a core Docker accepts.
	minCPUs = 0.01
	// minMemoryMB is the smallest memory limit worth setting: the daemon, a
	// shell, and git need about this much.
	minMemoryMB = 64
	// minPIDs is the smallest process limit the daemon and a shell fit in.
	minPIDs = 32
	// maxPIDs is the kernel's own ceiling on process ids.
	maxPIDs = 4194304
	// maxPorts is how many ports one workspace forwards.
	maxPorts = 20
	// maxPortLabel bounds a port's label.
	maxPortLabel = 40
	// defaultPIDs is the process limit a new workspace gets until the user
	// sets another: a fork bomb stops there, a parallel build does not.
	defaultPIDs = 4096
)

// sandboxBody is what a workspace's container may consume, reach, and
// expose.
type sandboxBody struct {
	Limits limitsBody `json:"limits"`
	Egress egressBody `json:"egress"`
	// Ports are the container ports the harness forwards previews to.
	Ports []portBody `json:"ports"`
}

// limitsBody bounds a container's resources. Zero is no limit.
type limitsBody struct {
	// CPUs is how many cores the container may use, as a fraction.
	CPUs float64 `json:"cpus"`
	// MemoryMB is its memory in MiB, with no swap beyond it.
	MemoryMB int64 `json:"memory_mb"`
	// PIDs is how many processes and threads it may have.
	PIDs int64 `json:"pids"`
}

// egressBody is what a container may reach on the network.
type egressBody struct {
	// Mode is open, allowlist, or none.
	Mode string `json:"mode"`
	// Allow is the host patterns an allowlist admits, such as github.com or
	// *.githubusercontent.com. It is kept whatever the mode, so switching
	// back to allowlist finds it as it was.
	Allow []string `json:"allow"`
}

// portBody is one container port the harness forwards to.
type portBody struct {
	Port int `json:"port"`
	// Label says what listens there.
	Label string `json:"label,omitempty"`
}

// usageResponse is the body of GET /api/workspaces/{id}/usage.
type usageResponse struct {
	// CPUPercent is the CPU the container used over the sample, 100 per core.
	CPUPercent float64 `json:"cpu_percent"`
	// CPUs is the cores it may use: its limit, or every core of the host.
	CPUs             float64 `json:"cpus"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	PIDs             int64   `json:"pids"`
	// PIDsLimit is its process limit, zero for none.
	PIDsLimit      int64     `json:"pids_limit"`
	NetworkRxBytes int64     `json:"network_rx_bytes"`
	NetworkTxBytes int64     `json:"network_tx_bytes"`
	SampledAt      time.Time `json:"sampled_at"`
	// Blocked are the hosts the egress proxy refused the workspace lately,
	// most recent first.
	Blocked []blockedBody `json:"blocked"`
}

// blockedBody is a host the egress proxy refused a workspace.
type blockedBody struct {
	Host  string    `json:"host"`
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

// sandboxDefaults are the limits and egress a new workspace gets when the
// request that creates it names none.
type sandboxDefaults struct {
	Limits limitsBody `json:"limits"`
	Egress egressBody `json:"egress"`
}

// handleSetWorkspaceSandbox records a workspace's new sandbox and applies it
// to the container, running or stopped, without restarting it.
func (s *Server) handleSetWorkspaceSandbox(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[sandboxBody](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sandbox, err := s.checkSandbox(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host, err := s.deps.Workspaces.Inspect(r.Context(), ws.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if host.State != workspace.StateRunning && host.State != workspace.StateStopped {
		s.fail(w, r, conflictf("workspace %s is %s", ws.ID, host.State))
		return
	}
	// The row is the desired state and is written first: a container that
	// took only part of the change gets the rest the next time it starts.
	if ws, err = s.deps.Store.SetWorkspaceSandbox(r.Context(), ws.ID, sandbox); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Workspaces.Confine(r.Context(), &host, confinement(sandbox)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.workspaceState(r.Context(), ws.ID, ws.ProjectID, ws.State)
	s.log.Info("workspace sandbox set", "workspace_id", ws.ID, "cpus", sandbox.CPUs,
		"memory_mb", sandbox.MemoryMB, "pids", sandbox.PIDs, "egress", egressMode(sandbox), "ports", len(sandbox.Ports))
	writeJSON(w, s.log, http.StatusOK, asWorkspace(ws))
}

// handleWorkspaceUsage samples what a running workspace is consuming, with
// the hosts its egress was refused lately.
func (s *Server) handleWorkspaceUsage(w http.ResponseWriter, r *http.Request) {
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host, err := s.deps.Workspaces.Inspect(r.Context(), ws.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if host.State != workspace.StateRunning {
		s.fail(w, r, conflictf("workspace %s is %s, not running", ws.ID, host.State))
		return
	}
	usage, err := s.deps.Workspaces.Usage(r.Context(), host)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := usageResponse{
		CPUPercent:       usage.CPUPercent,
		CPUs:             ws.Sandbox.CPUs,
		MemoryBytes:      usage.MemoryBytes,
		MemoryLimitBytes: usage.MemoryLimitBytes,
		PIDs:             usage.PIDs,
		PIDsLimit:        ws.Sandbox.PIDs,
		NetworkRxBytes:   usage.NetworkRxBytes,
		NetworkTxBytes:   usage.NetworkTxBytes,
		SampledAt:        usage.SampledAt,
		Blocked:          []blockedBody{},
	}
	if body.CPUs == 0 {
		if capacity, err := s.deps.Workspaces.Capacity(r.Context()); err == nil {
			body.CPUs = float64(capacity.CPUs)
		}
	}
	if s.deps.Egress != nil {
		for _, b := range s.deps.Egress.Blocked(ws.ID) {
			body.Blocked = append(body.Blocked, blockedBody{Host: b.Host, Count: b.Count, Last: b.Last})
		}
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// EgressPolicy is what the egress proxy asks on every request a sandbox
// sends it: the workspace's token, mode, and allowlist. A workspace is known
// by its hub token, which lives in its container, so the first request of
// each workspace reads it from there.
func (s *Server) EgressPolicy(ctx context.Context, id string) (egress.Policy, error) {
	ws, err := s.deps.Store.Workspace(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return egress.Policy{}, egress.ErrUnknownWorkspace
		}
		return egress.Policy{}, err
	}
	token, err := s.proxyToken(ctx, id)
	if err != nil {
		return egress.Policy{}, err
	}
	return egress.Policy{Token: token, Mode: egressMode(ws.Sandbox), Allow: ws.Sandbox.Allow}, nil
}

// proxyToken returns the token a workspace presents to the egress proxy.
// A container's hub token never changes and an id is never reused, so a
// token once read stays right until the workspace is destroyed.
func (s *Server) proxyToken(ctx context.Context, id string) (string, error) {
	s.tokensMu.Lock()
	token, ok := s.tokens[id]
	s.tokensMu.Unlock()
	if ok {
		return token, nil
	}
	host, err := s.deps.Workspaces.Inspect(ctx, id)
	if err != nil {
		if errors.Is(err, workspace.ErrNoWorkspace) {
			return "", egress.ErrUnknownWorkspace
		}
		return "", err
	}
	s.tokensMu.Lock()
	defer s.tokensMu.Unlock()
	if s.tokens == nil {
		s.tokens = map[string]string{}
	}
	s.tokens[id] = host.HubToken
	return host.HubToken, nil
}

// forgetEgress drops what the harness holds about a destroyed workspace's
// network access.
func (s *Server) forgetEgress(id string) {
	s.tokensMu.Lock()
	delete(s.tokens, id)
	s.tokensMu.Unlock()
	if s.deps.Egress != nil {
		s.deps.Egress.Forget(id)
	}
}

// newSandbox is the sandbox a workspace is created with: the one the request
// names, else the settings' defaults with no ports.
func (s *Server) newSandbox(ctx context.Context, req *sandboxBody) (store.WorkspaceSandbox, error) {
	if req != nil {
		return s.checkSandbox(ctx, *req)
	}
	defaults := s.sandboxDefaults(ctx)
	sandbox := fromBody(sandboxBody{Limits: defaults.Limits, Egress: defaults.Egress})
	// A default that asks for restricted egress on a harness that has none
	// is not the user's fault in this request; it is reported when saved.
	if egressMode(sandbox).Restricted() && !s.deps.Workspaces.EgressControl() {
		return store.WorkspaceSandbox{}, workspace.ErrNoEgressControl
	}
	return sandbox, nil
}

// checkSandbox validates a sandbox from a request against the Docker host's
// capacity and what it can restrict, and returns it in its stored form.
func (s *Server) checkSandbox(ctx context.Context, b sandboxBody) (store.WorkspaceSandbox, error) {
	if err := s.checkLimits(ctx, b.Limits); err != nil {
		return store.WorkspaceSandbox{}, err
	}
	allow, err := checkEgress(b.Egress)
	if err != nil {
		return store.WorkspaceSandbox{}, err
	}
	b.Egress.Allow = allow
	if b.Egress.Mode != string(egress.ModeOpen) && !s.deps.Workspaces.EgressControl() {
		return store.WorkspaceSandbox{}, workspace.ErrNoEgressControl
	}
	if len(b.Ports) > maxPorts {
		return store.WorkspaceSandbox{}, invalidf("a workspace forwards at most %d ports", maxPorts)
	}
	seen := map[int]bool{}
	for i, p := range b.Ports {
		switch {
		case p.Port < 1 || p.Port > 65535:
			return store.WorkspaceSandbox{}, invalidf("port %d is not a port from 1 to 65535", p.Port)
		case p.Port == daemonPort:
			return store.WorkspaceSandbox{}, invalidf("port %d is the sandbox daemon's own", p.Port)
		case seen[p.Port]:
			return store.WorkspaceSandbox{}, invalidf("port %d is listed twice", p.Port)
		case len(p.Label) > maxPortLabel:
			return store.WorkspaceSandbox{}, invalidf("the label of port %d is longer than %d characters", p.Port, maxPortLabel)
		}
		seen[p.Port] = true
		b.Ports[i].Label = strings.TrimSpace(p.Label)
	}
	return fromBody(b), nil
}

// daemonPort is the sandbox daemon's port, which is never forwarded.
const daemonPort = 7000

// checkLimits validates limits against the Docker host's capacity. A host
// that cannot be asked is not checked against: Docker refuses what it cannot
// give when the limits are applied.
func (s *Server) checkLimits(ctx context.Context, l limitsBody) error {
	capacity, err := s.deps.Workspaces.Capacity(ctx)
	if err != nil {
		s.log.Warn("read docker host capacity", "error", err)
		capacity = workspace.Capacity{CPUs: math.MaxInt32, MemoryBytes: math.MaxInt64}
	}
	switch {
	case l.CPUs != 0 && (math.IsNaN(l.CPUs) || l.CPUs < minCPUs || l.CPUs > float64(capacity.CPUs)):
		return invalidf("cpus must be 0 for no limit, or from %.2f to the host's %d cores", minCPUs, capacity.CPUs)
	case l.MemoryMB != 0 && (l.MemoryMB < minMemoryMB || l.MemoryMB > capacity.MemoryBytes>>20):
		return invalidf("memory_mb must be 0 for no limit, or from %d to the host's %d MiB", minMemoryMB, capacity.MemoryBytes>>20)
	case l.PIDs != 0 && (l.PIDs < minPIDs || l.PIDs > maxPIDs):
		return invalidf("pids must be 0 for no limit, or from %d to %d", minPIDs, maxPIDs)
	}
	return nil
}

// checkEgress validates an egress mode and allowlist, and returns the
// allowlist in the form it is stored: lowercase, trimmed, and without
// repeats.
func checkEgress(e egressBody) ([]string, error) {
	if _, err := egress.ParseMode(e.Mode); err != nil {
		return nil, invalidf("%v", err)
	}
	if len(e.Allow) > egress.MaxPatterns {
		return nil, invalidf("an allowlist holds at most %d hosts", egress.MaxPatterns)
	}
	allow := make([]string, 0, len(e.Allow))
	for _, p := range e.Allow {
		p = strings.ToLower(strings.TrimSpace(p))
		if err := egress.ValidPattern(p); err != nil {
			return nil, invalidf("%v", err)
		}
		if !slices.Contains(allow, p) {
			allow = append(allow, p)
		}
	}
	return allow, nil
}

// sandboxDefaults returns the limits and egress a new workspace gets: the
// settings' choice, or the harness's own.
func (s *Server) sandboxDefaults(ctx context.Context) sandboxDefaults {
	out := builtinSandboxDefaults()
	var limits limitsBody
	if readSetting(ctx, s.deps.Store, s.log, settingSandboxLimits, &limits) {
		out.Limits = limits
	}
	var e egressBody
	if readSetting(ctx, s.deps.Store, s.log, settingSandboxEgress, &e) {
		if _, err := egress.ParseMode(e.Mode); err == nil {
			out.Egress = egressBody{Mode: e.Mode, Allow: slices.Clone(e.Allow)}
		}
	}
	if out.Egress.Allow == nil {
		out.Egress.Allow = []string{}
	}
	return out
}

// builtinSandboxDefaults are the sandbox defaults until the user sets
// others: no CPU or memory limit, a process limit, and open egress with the
// suggested allowlist ready for when it is turned on.
func builtinSandboxDefaults() sandboxDefaults {
	return sandboxDefaults{
		Limits: limitsBody{PIDs: defaultPIDs},
		Egress: egressBody{Mode: string(egress.ModeOpen), Allow: slices.Clone(egress.DefaultAllowlist)},
	}
}

// childSandbox is the sandbox of a workspace made from another, a fork or a
// child agent's: the same limits and egress, and none of its ports, which
// belong to whatever the original was running.
func childSandbox(parent store.WorkspaceSandbox) store.WorkspaceSandbox {
	child := parent
	child.Allow = slices.Clone(parent.Allow)
	child.Ports = nil
	return child
}

// ChildSandbox is how a child agent's workspace is confined, which is the
// rule a fork follows too: the row its parent's sandbox gives, and the
// container that row asks for. The spawner is given it.
func ChildSandbox(parent store.WorkspaceSandbox) (store.WorkspaceSandbox, workspace.Confinement) {
	child := childSandbox(parent)
	return child, confinement(child)
}

// confinement is what a workspace's container is created and confined with.
func confinement(sb store.WorkspaceSandbox) workspace.Confinement {
	return workspace.Confinement{
		Limits: workspace.Limits{
			CPUs:        sb.CPUs,
			MemoryBytes: sb.MemoryMB << 20,
			PIDs:        sb.PIDs,
		},
		Proxied: egressMode(sb).Restricted(),
	}
}

// egressMode is a stored sandbox's egress mode; a workspace from before the
// modes is open.
func egressMode(sb store.WorkspaceSandbox) egress.Mode {
	if sb.Egress == "" {
		return egress.ModeOpen
	}
	return egress.Mode(sb.Egress)
}

// fromBody is a checked sandbox in its stored form.
func fromBody(b sandboxBody) store.WorkspaceSandbox {
	sb := store.WorkspaceSandbox{
		CPUs:     b.Limits.CPUs,
		MemoryMB: b.Limits.MemoryMB,
		PIDs:     b.Limits.PIDs,
		Egress:   b.Egress.Mode,
		Allow:    slices.Clone(b.Egress.Allow),
	}
	for _, p := range b.Ports {
		sb.Ports = append(sb.Ports, store.WorkspacePort{Port: p.Port, Label: p.Label})
	}
	return sb
}

// asSandbox renders a stored sandbox on the wire.
func asSandbox(sb store.WorkspaceSandbox) sandboxBody {
	b := sandboxBody{
		Limits: limitsBody{CPUs: sb.CPUs, MemoryMB: sb.MemoryMB, PIDs: sb.PIDs},
		Egress: egressBody{Mode: string(egressMode(sb)), Allow: slices.Clone(sb.Allow)},
		Ports:  make([]portBody, 0, len(sb.Ports)),
	}
	if b.Egress.Allow == nil {
		b.Egress.Allow = []string{}
	}
	for _, p := range sb.Ports {
		b.Ports = append(b.Ports, portBody{Port: p.Port, Label: p.Label})
	}
	return b
}

// sandboxPort reports whether a workspace forwards a port.
func sandboxPort(sb store.WorkspaceSandbox, port int) bool {
	return slices.ContainsFunc(sb.Ports, func(p store.WorkspacePort) bool { return p.Port == port })
}
