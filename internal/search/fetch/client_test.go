package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
)

func TestIsPublic(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "172.32.0.1", "192.169.0.1", "2606:4700::1111", "64:ff9b::808:808"} {
		if !isPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s is not public", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "10.0.0.5", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"192.0.2.1", "198.18.0.1", "240.0.0.1", "255.255.255.255", "224.0.0.1",
		"::1", "fe80::1", "fd00::1", "fec0::1", "2001:db8::1", "::ffff:127.0.0.1", "64:ff9b::a00:1",
	} {
		if isPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s is public", s)
		}
	}
}

func TestTheClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, err := NewClient().Get(srv.URL)
	if !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("Get(%s) = %v, want ErrPrivateAddress", srv.URL, err)
	}
	if got := transportError(err).Error(); got != "refusing to fetch a private address (127.0.0.1)" {
		t.Errorf("transportError = %q", got)
	}
}

func TestCheckRedirect(t *testing.T) {
	to := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	if err := checkRedirect(to("https://example.com/"), nil); err != nil {
		t.Errorf("an https redirect was refused: %v", err)
	}
	if err := checkRedirect(to("file:///etc/passwd"), nil); err == nil {
		t.Error("a file redirect was followed")
	}
	if err := checkRedirect(to("https://example.com/"), make([]*http.Request, maxRedirects)); err == nil {
		t.Error("a sixth redirect was followed")
	}
}
