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

func TestFixtureHostExceptionIsExact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port := u.Port()
	client := NewClient("localhost")
	resp, err := client.Get("http://localhost:" + port)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	resp.Body.Close()
	_, err = client.Get(srv.URL)
	if !errors.Is(err, netguard.ErrPrivateAddress) {
		t.Fatalf("unlisted address: %v", err)
	}
	if fixtureHost("sub.localhost", []string{"localhost"}) || fixtureHost("localhost.evil", []string{"localhost"}) {
		t.Fatal("matched a different hostname")
	}
}
