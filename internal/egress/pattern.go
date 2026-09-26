package egress

import (
	"fmt"
	"net/netip"
	"strings"
)

// Mode is what a workspace may reach on the network.
type Mode string

// The egress modes.
const (
	// ModeOpen gives the sandbox a route to the internet of its own.
	ModeOpen Mode = "open"
	// ModeAllowlist lets the sandbox reach the hosts its allowlist names,
	// through the proxy, and nothing else.
	ModeAllowlist Mode = "allowlist"
	// ModeNone lets the sandbox reach the harness alone: the git hub, and
	// nothing on the internet.
	ModeNone Mode = "none"
)

// Modes lists every mode, in the order the UI offers them.
var Modes = []Mode{ModeOpen, ModeAllowlist, ModeNone}

// Restricted reports whether a mode keeps the sandbox off the open network,
// so that it reaches out only through the proxy.
func (m Mode) Restricted() bool { return m == ModeAllowlist || m == ModeNone }

// ParseMode reads a mode by name.
func ParseMode(s string) (Mode, error) {
	for _, m := range Modes {
		if string(m) == s {
			return m, nil
		}
	}
	return "", fmt.Errorf("egress mode %q is not one of open, allowlist, or none", s)
}

// Bounds on an allowlist.
const (
	// MaxPatterns is how many patterns one allowlist holds.
	MaxPatterns = 200
	// maxPatternLength is the longest pattern: a whole DNS name.
	maxPatternLength = 253
)

// DefaultAllowlist is the allowlist a deployment starts with: the git forges
// and package registries an agent needs to fetch code and dependencies. It
// is a suggestion the user edits in the settings, not a rule.
var DefaultAllowlist = []string{
	"github.com",
	"*.github.com",
	"*.githubusercontent.com",
	"gitlab.com",
	"registry.npmjs.org",
	"pypi.org",
	"files.pythonhosted.org",
	"proxy.golang.org",
	"sum.golang.org",
	"crates.io",
	"*.crates.io",
}

// ValidPattern checks one allowlist pattern: a host name such as github.com,
// a wildcard such as *.github.com that matches every name below it but not
// the name itself, or an IP address. A pattern names a host, never a scheme,
// a port, or a path, and a lone * is refused: that is the open mode.
func ValidPattern(p string) error {
	if p == "" || len(p) > maxPatternLength {
		return fmt.Errorf("allowlist entry %q must be a host name of 1 to %d characters", p, maxPatternLength)
	}
	if _, err := netip.ParseAddr(p); err == nil {
		return nil
	}
	name := strings.TrimPrefix(p, "*.")
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("allowlist entry %q is not a host name such as github.com or *.github.com", p)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("allowlist entry %q is not a lowercase host name such as github.com or *.github.com", p)
			}
		}
	}
	return nil
}

// Allowed reports whether host matches a pattern in allow. Host names are
// compared without case and without a trailing dot.
func Allowed(allow []string, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if addr, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		host = addr.Unmap().String()
	}
	for _, p := range allow {
		if suffix, ok := strings.CutPrefix(p, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == p {
			return true
		}
	}
	return false
}
