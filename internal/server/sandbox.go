package server

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/store"
)

// Bounds on a computer's sandbox. The limits' upper bounds are the Docker
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
	// defaultPIDs is the process limit the home computer gets until the user
	// sets another: a fork bomb stops there, a parallel build does not.
	defaultPIDs = 4096
)

// sandboxBody is what a computer's container may consume and reach.
type sandboxBody struct {
	Limits limitsBody `json:"limits"`
	Egress egressBody `json:"egress"`
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

// sandboxDefaults are the limits and egress the home computer gets until the
// user sets others.
type sandboxDefaults struct {
	Limits limitsBody `json:"limits"`
	Egress egressBody `json:"egress"`
}

// EgressPolicy is what the egress proxy asks on every request a sandbox
// sends it: the computer's token, mode, and allowlist. A computer is known
// by its proxy token, which lives in its container's configuration, so the
// first request of each computer reads it from there.
func (s *Server) EgressPolicy(ctx context.Context, id string) (egress.Policy, error) {
	cx, err := s.deps.Store.Computer(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return egress.Policy{}, egress.ErrUnknownComputer
		}
		return egress.Policy{}, err
	}
	token, err := s.proxyToken(ctx, id)
	if err != nil {
		return egress.Policy{}, err
	}
	return egress.Policy{Token: token, Mode: egressMode(cx.Sandbox), Allow: cx.Sandbox.Allow}, nil
}

// proxyTokens holds the egress proxy token of each computer the proxy was
// asked about.
type proxyTokens struct {
	mu         sync.Mutex
	byComputer map[string]string
}

// proxyToken returns the token a computer presents to the egress proxy.
// A container's proxy token never changes, so a token once read stays right
// until the container is replaced, which forgetEgress is called for.
func (s *Server) proxyToken(ctx context.Context, id string) (string, error) {
	s.proxyTokens.mu.Lock()
	token, ok := s.proxyTokens.byComputer[id]
	s.proxyTokens.mu.Unlock()
	if ok {
		return token, nil
	}
	host, err := s.deps.Computers.Inspect(ctx, id)
	if err != nil {
		if errors.Is(err, computer.ErrNoComputer) {
			return "", egress.ErrUnknownComputer
		}
		return "", err
	}
	s.proxyTokens.mu.Lock()
	defer s.proxyTokens.mu.Unlock()
	if s.proxyTokens.byComputer == nil {
		s.proxyTokens.byComputer = map[string]string{}
	}
	s.proxyTokens.byComputer[id] = host.ProxyToken
	return host.ProxyToken, nil
}

// forgetEgress drops what the harness holds about a computer's network access
// when its container is replaced or destroyed.
func (s *Server) forgetEgress(id string) {
	s.proxyTokens.mu.Lock()
	delete(s.proxyTokens.byComputer, id)
	s.proxyTokens.mu.Unlock()
	if s.deps.Egress != nil {
		s.deps.Egress.Forget(id)
	}
}

// homeSandbox is the sandbox the home computer has: what the settings say,
// or the harness's own defaults. The settings were checked when they were
// written, so a restricted egress on a harness that cannot restrict it can
// only mean the harness changed since, which is an error here.
func (s *Server) homeSandbox(ctx context.Context) (store.ComputerSandbox, error) {
	sandbox := fromBody(sandboxBody(s.sandboxDefaults(ctx)))
	if egressMode(sandbox).Restricted() && !s.deps.Computers.EgressControl() {
		return store.ComputerSandbox{}, computer.ErrNoEgressControl
	}
	return sandbox, nil
}

// checkLimits validates limits against the Docker host's capacity. A host
// that cannot be asked is not checked against: Docker refuses what it cannot
// give when the limits are applied.
func (s *Server) checkLimits(ctx context.Context, l limitsBody) error {
	capacity, err := s.deps.Computers.Capacity(ctx)
	if err != nil {
		s.log.Warn("read docker host capacity", "error", err)
		capacity = computer.Capacity{CPUs: math.MaxInt32, MemoryBytes: math.MaxInt64}
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

// sandboxDefaults returns the limits and egress the home computer gets: the
// settings' choice, or the harness's own.
func (s *Server) sandboxDefaults(ctx context.Context) sandboxDefaults {
	out := builtinSandboxDefaults()
	var limits limitsBody
	if readSetting(ctx, s.deps.Store, s.log, settingSandboxLimits, &limits) {
		out.Limits = limits
	}
	var e egressBody
	if readSetting(ctx, s.deps.Store, s.log, settingSandboxEgress, &e) {
		if allow, err := checkEgress(e); err == nil {
			out.Egress = egressBody{Mode: e.Mode, Allow: allow}
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

// confinement is what a computer's container is created and confined with.
func confinement(sb store.ComputerSandbox) computer.Confinement {
	return computer.Confinement{
		Limits: computer.Limits{
			CPUs:        sb.CPUs,
			MemoryBytes: sb.MemoryMB << 20,
			PIDs:        sb.PIDs,
		},
		Proxied: egressMode(sb).Restricted(),
	}
}

// egressMode is a stored sandbox's egress mode; a computer from before the
// modes is open.
func egressMode(sb store.ComputerSandbox) egress.Mode {
	if sb.Egress == "" {
		return egress.ModeOpen
	}
	return egress.Mode(sb.Egress)
}

// fromBody is a checked sandbox in its stored form.
func fromBody(b sandboxBody) store.ComputerSandbox {
	sb := store.ComputerSandbox{
		CPUs:     b.Limits.CPUs,
		MemoryMB: b.Limits.MemoryMB,
		PIDs:     b.Limits.PIDs,
		Egress:   b.Egress.Mode,
		Allow:    slices.Clone(b.Egress.Allow),
	}
	return sb
}

// asSandbox renders a stored sandbox on the wire.
func asSandbox(sb store.ComputerSandbox) sandboxBody {
	b := sandboxBody{
		Limits: limitsBody{CPUs: sb.CPUs, MemoryMB: sb.MemoryMB, PIDs: sb.PIDs},
		Egress: egressBody{Mode: string(egressMode(sb)), Allow: slices.Clone(sb.Allow)},
	}
	if b.Egress.Allow == nil {
		b.Egress.Allow = []string{}
	}
	return b
}
