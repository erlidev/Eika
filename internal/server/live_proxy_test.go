//go:build live

package server

import (
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/netguard"
)

func TestLiveProxyFixtureIsLimitedToItsLoopbackPort(t *testing.T) {
	t.Setenv("EIKA_LIVE_PROXY_FIXTURE_PORT", "8123")
	control := liveProxyControl(true)
	if err := control("tcp", "127.0.0.1:8123", nil); err != nil {
		t.Fatal(err)
	}
	if err := control("tcp", "127.0.0.1:8124", nil); !errors.Is(err, netguard.ErrPrivateAddress) {
		t.Fatalf("other port: %v", err)
	}
	if liveProxyControl(false) != nil {
		t.Fatal("control enabled outside live test")
	}
}
