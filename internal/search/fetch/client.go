package fetch

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// maxRedirects is how many redirects a fetch follows.
const maxRedirects = 5

// ErrPrivateAddress reports a fetch that would reach an address that is not
// on the public internet: loopback, a private or shared range, link-local,
// or a reserved block.
var ErrPrivateAddress = errors.New("refusing to connect to a non-public address")

// NewClient returns the client a fetch goes through: it connects to public
// addresses only, uses no proxy, and follows a few http and https redirects.
// The Reader bounds each request's time itself.
func NewClient() *http.Client {
	return newClient(publicOnly)
}

// newClient builds the fetch client around a dial check.
func newClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: control}
	return &http.Client{
		Transport: &http.Transport{
			// No proxy: the address check applies to the connection the
			// fetch makes, and a proxy would make that connection instead.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
		},
		CheckRedirect: checkRedirect,
	}
}

// checkRedirect follows a bounded number of redirects to http and https
// URLs. The address check runs again on the connection each one makes.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing to follow a redirect to %s", req.URL.Scheme)
	}
	return nil
}

// publicOnly refuses a connection to an address that is not public. It runs
// on the resolved address the dialer is about to connect to.
func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("check address %s: %w", address, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("check address %s: %w", address, err)
	}
	if !isPublic(addr) {
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

// isPublic reports whether addr is on the public internet.
func isPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if nat64.Contains(addr) {
		b := addr.As16()
		return isPublic(netip.AddrFrom4([4]byte(b[12:])))
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
