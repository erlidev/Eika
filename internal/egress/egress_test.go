package egress_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/egress"
)

func TestValidPattern(t *testing.T) {
	for _, p := range []string{"github.com", "*.github.com", "a-b.example.co", "localhost", "203.0.113.5", "2001:db8::1"} {
		if err := egress.ValidPattern(p); err != nil {
			t.Errorf("ValidPattern(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{"", "*", "https://github.com", "github.com:443", "github.com/org", "GitHub.com", "*.", "a..b", "-a.com", "a-.com", "*.*.com", "exa mple.com", strings.Repeat("a", 254)} {
		if err := egress.ValidPattern(p); err == nil {
			t.Errorf("ValidPattern(%q) = nil, want an error", p)
		}
	}
}

func TestAllowed(t *testing.T) {
	allow := []string{"github.com", "*.githubusercontent.com", "203.0.113.5"}
	cases := map[string]bool{
		"github.com":                          true,
		"GitHub.com.":                         true,
		"api.github.com":                      false,
		"raw.githubusercontent.com":           true,
		"a.b.githubusercontent.com":           true,
		"githubusercontent.com":               false,
		"evilgithubusercontent.com":           false,
		"github.com.evil.example":             false,
		"203.0.113.5":                         true,
		"::ffff:203.0.113.5":                  true,
		"203.0.113.6":                         false,
		"notgithub.com":                       false,
		"raw.githubusercontent.com.attacker.": false,
	}
	for host, want := range cases {
		if got := egress.Allowed(allow, host); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", host, got, want)
		}
	}
	if egress.Allowed(nil, "github.com") {
		t.Error("an empty allowlist allowed a host")
	}
}

func TestParseMode(t *testing.T) {
	for _, m := range egress.Modes {
		if got, err := egress.ParseMode(string(m)); err != nil || got != m {
			t.Errorf("ParseMode(%q) = %q, %v", m, got, err)
		}
	}
	if _, err := egress.ParseMode("closed"); err == nil {
		t.Error("ParseMode accepted an unknown mode")
	}
	if egress.ModeOpen.Restricted() || !egress.ModeAllowlist.Restricted() || !egress.ModeNone.Restricted() {
		t.Error("only allowlist and none keep a sandbox off the open network")
	}
}

// anyAddress lets the proxy reach the tests' servers, which listen on
// loopback.
func anyAddress(string, string, syscall.RawConn) error { return nil }

// policies is a fixed table of workspaces the proxy looks up.
type policies map[string]egress.Policy

func (p policies) lookup(_ context.Context, id string) (egress.Policy, error) {
	policy, ok := p[id]
	if !ok {
		return egress.Policy{}, egress.ErrUnknownWorkspace
	}
	return policy, nil
}

// serveProxy runs a proxy over a policy table and returns its address.
func serveProxy(t *testing.T, table policies, control func(string, string, syscall.RawConn) error) (*egress.Proxy, string) {
	t.Helper()
	p := egress.New(egress.Options{Policy: table.lookup, Control: control}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	t.Cleanup(p.Close)
	return p, srv.URL
}

// through returns a client that sends every request through the proxy as
// the given workspace. base is the client of the server the test reaches,
// so an https test trusts its certificate.
func through(t *testing.T, base *http.Client, proxy, user, password string) *http.Client {
	t.Helper()
	u, err := url.Parse(proxy)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	transport := base.Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(u)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func TestTheProxyTunnelsToAnAllowedHost(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello over tls")
	}))
	defer target.Close()
	_, proxy := serveProxy(t, policies{"ws1": {Token: "tok", Mode: egress.ModeAllowlist, Allow: []string{"127.0.0.1"}}}, anyAddress)

	resp, err := through(t, target.Client(), proxy, "ws1", "tok").Get(target.URL)
	if err != nil {
		t.Fatalf("get through the tunnel: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello over tls" {
		t.Errorf("body = %q", body)
	}
}

func TestTheProxyForwardsPlainHTTP(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The upstream never sees the sandbox's proxy credentials.
		if r.Header.Get("Proxy-Authorization") != "" {
			http.Error(w, "credentials leaked", http.StatusTeapot)
			return
		}
		io.WriteString(w, "hello "+r.URL.Path)
	}))
	defer target.Close()
	_, proxy := serveProxy(t, policies{"ws1": {Token: "tok", Mode: egress.ModeAllowlist, Allow: []string{"127.0.0.1"}}}, anyAddress)

	resp, err := through(t, target.Client(), proxy, "ws1", "tok").Get(target.URL + "/path")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello /path" {
		t.Errorf("status %d, body %q", resp.StatusCode, body)
	}
}

func TestTheProxyRefuses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	table := policies{
		"listed": {Token: "tok", Mode: egress.ModeAllowlist, Allow: []string{"example.com"}},
		"off":    {Token: "tok", Mode: egress.ModeNone, Allow: []string{"127.0.0.1"}},
		"open":   {Token: "tok", Mode: egress.ModeOpen},
	}
	p, proxy := serveProxy(t, table, anyAddress)
	_, guarded := serveProxy(t, table, nil)

	cases := []struct {
		name, proxy, user, password string
		status                      int
	}{
		{"a request without credentials", proxy, "", "", http.StatusProxyAuthRequired},
		{"a wrong token", proxy, "listed", "nope", http.StatusProxyAuthRequired},
		{"an unknown workspace", proxy, "gone", "tok", http.StatusProxyAuthRequired},
		{"a host not on the allowlist", proxy, "listed", "tok", http.StatusForbidden},
		{"a workspace whose egress is off", proxy, "off", "tok", http.StatusForbidden},
		{"a private address, whatever the mode", guarded, "open", "tok", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := through(t, target.Client(), c.proxy, c.user, c.password).Get(target.URL)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != c.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.status)
			}
		})
	}

	blocked := p.Blocked("listed")
	if len(blocked) != 1 || blocked[0].Host != "127.0.0.1" || blocked[0].Count != 1 || blocked[0].Last.IsZero() {
		t.Errorf("blocked = %+v, want the one refused host", blocked)
	}
	if blocked := p.Blocked("off"); len(blocked) != 1 {
		t.Errorf("blocked = %+v, want the host refused while egress was off", blocked)
	}
	p.Forget("listed")
	if blocked := p.Blocked("listed"); len(blocked) != 0 {
		t.Errorf("blocked after Forget = %+v, want none", blocked)
	}
}

func TestBlockedKeepsTheMostRecentHostsFirst(t *testing.T) {
	p, proxy := serveProxy(t, policies{"ws1": {Token: "tok", Mode: egress.ModeAllowlist}}, anyAddress)
	client := through(t, &http.Client{Transport: &http.Transport{}}, proxy, "ws1", "tok")
	for _, host := range []string{"a.example", "b.example", "a.example"} {
		resp, err := client.Get("http://" + host + "/")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
	}
	blocked := p.Blocked("ws1")
	if len(blocked) != 2 || blocked[0].Host != "a.example" || blocked[0].Count != 2 || blocked[1].Host != "b.example" {
		t.Errorf("blocked = %+v, want a.example twice, then b.example", blocked)
	}
}

func TestCloseEndsOpenTunnels(t *testing.T) {
	// The destination accepts and then says nothing, so the tunnel stays
	// open until something ends it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	p, proxy := serveProxy(t, policies{"ws1": {Token: "tok", Mode: egress.ModeOpen}}, anyAddress)

	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	req, _ := http.NewRequest(http.MethodConnect, "", nil)
	req.Host = ln.Addr().String()
	req.SetBasicAuth("ws1", "tok")
	req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
	req.Header.Del("Authorization")
	if err := req.Write(conn); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("connect = %v, %v; want 200", resp, err)
	}

	p.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Errorf("read after Close = %v, want the tunnel closed", err)
	}
}
