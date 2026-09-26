package netguard_test

import (
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/erlidev/eika/internal/netguard"
)

func TestIsPublic(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "172.32.0.1", "192.169.0.1", "2606:4700::1111", "64:ff9b::808:808"} {
		if !netguard.IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s is not public", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "10.0.0.5", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"192.0.2.1", "198.18.0.1", "240.0.0.1", "255.255.255.255", "224.0.0.1",
		"::1", "fe80::1", "fd00::1", "fec0::1", "2001:db8::1", "::ffff:127.0.0.1", "64:ff9b::a00:1",
	} {
		if netguard.IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s is public", s)
		}
	}
}

func TestADialerWithControlRefusesPrivateAddresses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	dialer := net.Dialer{Control: netguard.Control}
	conn, err := dialer.DialContext(t.Context(), "tcp", ln.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("the dialer connected to loopback")
	}
	if !errors.Is(err, netguard.ErrPrivateAddress) {
		t.Errorf("dial = %v, want ErrPrivateAddress", err)
	}
}
