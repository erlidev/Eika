package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/erlidev/eika/internal/netguard"
)

func TestTheClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, err := NewClient().Get(srv.URL)
	if !errors.Is(err, netguard.ErrPrivateAddress) {
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
