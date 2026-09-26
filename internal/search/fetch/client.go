package fetch

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/erlidev/eika/internal/netguard"
)

// maxRedirects is how many redirects a fetch follows.
const maxRedirects = 5

// NewClient returns the client a fetch goes through: it connects to public
// addresses only, uses no proxy, and follows a few http and https redirects.
// The Reader bounds each request's time itself.
func NewClient() *http.Client {
	return newClient(netguard.Control)
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
