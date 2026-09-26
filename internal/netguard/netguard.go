package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// ErrPrivateAddress reports a connection that would reach an address that is
// not on the public internet: loopback, a private or shared range,
// link-local, or a reserved block.
var ErrPrivateAddress = errors.New("refusing to connect to a non-public address")

// Control refuses a connection to an address that is not public. It is a
// net.Dialer Control function, so it runs on the resolved address the dialer
// is about to connect to.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("check address %s: %w", address, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("check address %s: %w", address, err)
	}
	if !IsPublic(addr) {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, addr)
	}
	return nil
}

// nonPublic are the blocks netip's predicates do not already exclude.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, shared address space
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),  // documentation
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, and the broadcast address
	netip.MustParsePrefix("fec0::/10"),     // deprecated site-local
	netip.MustParsePrefix("2001:db8::/32"), // documentation
}

// nat64 is the well-known NAT64 prefix. An address in it reaches the IPv4
// address in its last four bytes, which must be public in turn.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// IsPublic reports whether addr is on the public internet.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if nat64.Contains(addr) {
		b := addr.As16()
		return IsPublic(netip.AddrFrom4([4]byte(b[12:])))
	}
	// IsGlobalUnicast already rules out loopback, link-local, multicast, and
	// the unspecified address.
	if !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}
