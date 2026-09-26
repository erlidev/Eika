package egress

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/erlidev/eika/internal/netguard"
)

// ErrUnknownWorkspace is what Options.Policy returns for a workspace that
// does not exist, which the proxy answers as a failed sign-in.
var ErrUnknownWorkspace = errors.New("unknown workspace")

// Bounds on the proxy's own connections.
const (
	// dialTimeout bounds connecting to a destination.
	dialTimeout = 10 * time.Second
	// maxBlocked is how many refused hosts are remembered per workspace.
	maxBlocked = 20
)

// Policy is what one workspace may reach through the proxy.
type Policy struct {
	// Token is the credential the workspace presents: its hub token.
	Token string
	// Mode is the workspace's egress mode.
	Mode Mode
	// Allow is the allowlist, used in ModeAllowlist.
	Allow []string
}

// Options configures a Proxy.
type Options struct {
	// Policy looks up the policy of the workspace a request names. It is
	// asked on every request, so a change applies to the next connection.
	Policy func(ctx context.Context, workspaceID string) (Policy, error)
	// Control checks each address the proxy is about to connect to. Nil is
	// netguard.Control, which refuses every address that is not public; only
	// tests, whose servers listen on loopback, set another.
	Control func(network, address string, c syscall.RawConn) error
}

// Blocked is a host a workspace was refused.
type Blocked struct {
	// Host is the host name or address the sandbox asked for.
	Host string
	// Count is how many requests for it were refused since the harness
	// started.
	Count int
	// Last is when the most recent one was, in UTC.
	Last time.Time
}

// Proxy is the HTTP proxy restricted sandboxes reach the internet through.
// It is an http.Handler; Close ends the tunnels it has open.
type Proxy struct {
	opts    Options
	log     *slog.Logger
	dialer  *net.Dialer
	forward *httputil.ReverseProxy

	// mu guards everything below it.
	mu sync.Mutex
	// tunnels are the CONNECT tunnels open now, closed by Close.
	tunnels map[net.Conn]struct{}
	// blocked holds each workspace's refused hosts, most recent last.
	blocked map[string][]Blocked
}

// New builds a Proxy.
func New(opts Options, log *slog.Logger) *Proxy {
	control := opts.Control
	if control == nil {
		control = netguard.Control
	}
	p := &Proxy{
		opts:    opts,
		log:     log,
		dialer:  &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, Control: control},
		tunnels: map[net.Conn]struct{}{},
		blocked: map[string][]Blocked{},
	}
	p.forward = &httputil.ReverseProxy{
		// The request is already in proxy form, an absolute URL, so it goes
		// where it names. ReverseProxy drops the hop-by-hop headers, the
		// proxy credentials among them.
		Rewrite: func(*httputil.ProxyRequest) {},
		Transport: &http.Transport{
			// Never another proxy: the address check applies to the
			// connection this one makes.
			Proxy:                 nil,
			DialContext:           p.dialer.DialContext,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Minute,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			refuse(w, dialStatus(err), err.Error())
		},
	}
	return p
}

// ServeHTTP serves one proxy request: a CONNECT tunnel, or a plain http
// request in absolute form.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, token, ok := credentials(r)
	if !ok {
		challenge(w, "the egress proxy needs a workspace's credentials")
		return
	}
	policy, err := p.opts.Policy(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUnknownWorkspace) {
			challenge(w, "unknown workspace")
			return
		}
		p.log.Error("read egress policy", "workspace_id", id, "error", err)
		refuse(w, http.StatusBadGateway, "the egress proxy could not read the workspace's policy")
		return
	}
	if policy.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(policy.Token)) != 1 {
		challenge(w, "wrong credentials for workspace "+id)
		return
	}

	target := r.Host
	if r.Method != http.MethodConnect {
		if r.URL.Scheme != "http" || r.URL.Host == "" {
			refuse(w, http.StatusBadRequest, "the egress proxy takes CONNECT, or an absolute http URL")
			return
		}
		target = r.URL.Host
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	switch {
	case policy.Mode == ModeNone:
		p.block(id, host)
		refuse(w, http.StatusForbidden, "this workspace has no network access: its egress is off")
		return
	case policy.Mode == ModeAllowlist && !Allowed(policy.Allow, host):
		p.block(id, host)
		refuse(w, http.StatusForbidden, fmt.Sprintf("%s is not on this workspace's allowlist; add it in the workspace's Sandbox panel", host))
		return
	}

	if r.Method == http.MethodConnect {
		p.tunnel(w, r)
		return
	}
	p.forward.ServeHTTP(w, r)
}

// Blocked lists the hosts a workspace was refused lately, most recent first.
func (p *Proxy) Blocked(workspaceID string) []Blocked {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.blocked[workspaceID])
	slices.Reverse(out)
	return out
}

// Forget drops what the proxy remembers about a workspace, once it is gone.
func (p *Proxy) Forget(workspaceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.blocked, workspaceID)
}

// Close ends every tunnel that is open. The server that serves the proxy
// does not track a hijacked connection, so its shutdown alone leaves them.
func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for conn := range p.tunnels {
		conn.Close()
	}
	p.tunnels = nil
}

// tunnel connects to the CONNECT request's destination and relays bytes both
// ways until either side closes.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := p.dialer.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		refuse(w, dialStatus(err), err.Error())
		return
	}
	client, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upstream.Close()
		refuse(w, http.StatusInternalServerError, "the egress proxy could not take over the connection")
		return
	}
	if !p.track(client, upstream) {
		client.Close()
		upstream.Close()
		return
	}
	defer p.untrack(client, upstream)

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	// Bytes the client sent after its request line are already read into
	// the server's buffer.
	if n := buffered.Reader.Buffered(); n > 0 {
		head, _ := buffered.Reader.Peek(n)
		if _, err := upstream.Write(head); err != nil {
			return
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		io.Copy(upstream, client)
		closeWrite(upstream)
	})
	wg.Go(func() {
		io.Copy(client, upstream)
		closeWrite(client)
	})
	wg.Wait()
}

// track records a tunnel's two connections so Close can end them. It
// refuses once Close has run.
func (p *Proxy) track(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tunnels == nil {
		return false
	}
	for _, c := range conns {
		p.tunnels[c] = struct{}{}
	}
	return true
}

// untrack closes a finished tunnel's connections and forgets them.
func (p *Proxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		c.Close()
		delete(p.tunnels, c)
	}
}

// block records that a workspace was refused a host, and logs the first
// refusal of each host it remembers.
func (p *Proxy) block(workspaceID, host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := p.blocked[workspaceID]
	now := time.Now().UTC()
	i := slices.IndexFunc(list, func(b Blocked) bool { return b.Host == host })
	if i >= 0 {
		b := list[i]
		b.Count++
		b.Last = now
		list = append(slices.Delete(list, i, i+1), b)
	} else {
		p.log.Info("egress refused", "workspace_id", workspaceID, "host", host)
		list = append(list, Blocked{Host: host, Count: 1, Last: now})
		if len(list) > maxBlocked {
			list = list[len(list)-maxBlocked:]
		}
	}
	p.blocked[workspaceID] = list
}

// credentials reads the workspace id and token from Proxy-Authorization.
func credentials(r *http.Request) (string, string, bool) {
	h := r.Header.Get("Proxy-Authorization")
	if h == "" {
		return "", "", false
	}
	// net/http parses Basic credentials from Authorization only.
	probe := &http.Request{Header: http.Header{"Authorization": {h}}}
	return probe.BasicAuth()
}

// challenge answers a request without usable credentials.
func challenge(w http.ResponseWriter, msg string) {
	w.Header().Set("Proxy-Authenticate", `Basic realm="eika egress"`)
	refuse(w, http.StatusProxyAuthRequired, msg)
}

// refuse answers with a status and a one-line reason, which is what curl,
// git, and npm show the person reading their output.
func refuse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	io.WriteString(w, "eika egress: "+msg+"\n")
}

// dialStatus is the status a failed connection is answered with: 403 for a
// destination the address check refused, 502 for one that could not be
// reached.
func dialStatus(err error) int {
	if errors.Is(err, netguard.ErrPrivateAddress) {
		return http.StatusForbidden
	}
	return http.StatusBadGateway
}

// closeWrite half-closes a connection, telling the far side no more bytes
// are coming while the other direction finishes.
func closeWrite(c net.Conn) {
	if tcp, ok := c.(interface{ CloseWrite() error }); ok {
		tcp.CloseWrite()
		return
	}
	c.Close()
}
