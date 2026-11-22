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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/erlidev/eika/internal/netguard"
)

// ErrUnknownComputer is what Options.Policy returns for a computer that
// does not exist, which the proxy answers as a failed sign-in.
var ErrUnknownComputer = errors.New("unknown computer")

var errHalted = errors.New("egress halted")

// Bounds on the proxy's own connections.
const (
	// dialTimeout bounds connecting to a destination.
	dialTimeout = 10 * time.Second
	// maxBlocked is how many refused hosts are remembered per computer.
	maxBlocked = 20
)

// Policy is what one computer may reach through the proxy.
type Policy struct {
	// Token is the credential the computer presents: its proxy token.
	Token string
	// Mode is the computer's egress mode.
	Mode Mode
	// Allow is the allowlist, used in ModeAllowlist.
	Allow []string
}

// Options configures a Proxy.
type Options struct {
	// Policy looks up the policy of the computer a request names. It is
	// asked on every request, so a change applies to the next connection.
	Policy func(ctx context.Context, computerID string) (Policy, error)
	// Control checks each address the proxy is about to connect to. Nil is
	// netguard.Control, which refuses every address that is not public; only
	// tests, whose servers listen on loopback, set another.
	Control func(network, address string, c syscall.RawConn) error
	// Decide checks one connection, including any proxy refusal.
	Decide func(context.Context, string, string, string, string) (bool, error)
	// Halted reports the current halt state.
	Halted func() bool
}

// Blocked is a host a computer was refused.
type Blocked struct {
	// Host is the host name or address the sandbox asked for.
	Host string
	// Count is how many requests for it were refused since the harness
	// started.
	Count int
	// Last is when the most recent one was, in UTC.
	Last time.Time
}

// Proxy is the HTTP proxy sandboxes reach the internet through.
// It is an http.Handler; Close ends the tunnels it has open.
type Proxy struct {
	opts     Options
	log      *slog.Logger
	dialer   *net.Dialer
	forward  *httputil.ReverseProxy
	sequence atomic.Uint64

	// mu guards everything below it.
	mu sync.Mutex
	// tunnels are the CONNECT tunnels open now, closed by Close.
	tunnels map[net.Conn]struct{}
	// blocked holds each computer's refused hosts, most recent last.
	blocked    map[string][]Blocked
	generation uint64
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
			DialContext:           p.dialHTTP,
			DisableKeepAlives:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Minute,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, netguard.ErrPrivateAddress) {
				p.denied(r.Context(), "non-public address")
			}
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
		challenge(w, "the egress proxy needs a computer's credentials")
		return
	}
	policy, err := p.opts.Policy(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUnknownComputer) {
			challenge(w, "unknown computer")
			return
		}
		p.log.Error("read egress policy", "computer_id", id, "error", err)
		refuse(w, http.StatusBadGateway, "the egress proxy could not read the computer's policy")
		return
	}
	if policy.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(policy.Token)) != 1 {
		challenge(w, "wrong credentials for computer "+id)
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
		host = strings.Trim(target, "[]")
	}
	reason := ""
	if policy.Mode == ModeNone {
		reason = "network access is off"
	}
	if policy.Mode == ModeAllowlist && !Allowed(policy.Allow, host) {
		reason = "host is not on the allowlist"
	}
	info := connection{id: id, host: host, key: fmt.Sprintf("%s-%d-%d", id, time.Now().UnixNano(), p.sequence.Add(1)), generation: p.currentGeneration()}
	r = r.WithContext(context.WithValue(r.Context(), connectionKey{}, info))
	if p.opts.Decide != nil {
		allowed, err := p.opts.Decide(r.Context(), info.id, info.host, info.key, reason)
		if err != nil {
			p.log.Error("decide egress", "error", err)
			refuse(w, http.StatusBadGateway, "could not check egress")
			return
		}
		if !allowed {
			p.block(id, host)
			refuse(w, http.StatusForbidden, "connection refused")
			return
		}
	} else if reason != "" || p.isHalted() {
		p.block(id, host)
		refuse(w, http.StatusForbidden, "connection refused")
		return
	}

	if !p.validGeneration(info.generation) {
		p.denied(r.Context(), "Eika is halted")
		refuse(w, http.StatusForbidden, "connection refused")
		return
	}

	if r.Method == http.MethodConnect {
		p.tunnel(w, r, info.generation)
		return
	}
	p.forward.ServeHTTP(w, r)
}

type connectionKey struct{}
type connection struct {
	id, host, key string
	generation    uint64
}

func (p *Proxy) isHalted() bool { return p.opts.Halted != nil && p.opts.Halted() }

func (p *Proxy) denied(ctx context.Context, reason string) {
	info, ok := ctx.Value(connectionKey{}).(connection)
	if !ok {
		return
	}
	p.block(info.id, info.host)
	if p.opts.Decide != nil {
		if _, err := p.opts.Decide(ctx, info.id, info.host, info.key, reason); err != nil {
			p.log.Error("audit egress refusal", "error", err)
		}
	}
}

// Halt closes open connections. New connections are refused while Halted is true.
func (p *Proxy) Halt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	for conn := range p.tunnels {
		_ = conn.Close()
	}
}

type trackedConn struct {
	net.Conn
	p *Proxy
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.p.mu.Lock()
	delete(c.p.tunnels, c.Conn)
	c.p.mu.Unlock()
	return err
}
func (p *Proxy) dialHTTP(ctx context.Context, network, address string) (net.Conn, error) {
	info, ok := ctx.Value(connectionKey{}).(connection)
	if !ok {
		return nil, errHalted
	}
	conn, err := p.dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if !p.trackGeneration(info.generation, conn) {
		p.denied(ctx, "Eika is halted")
		_ = conn.Close()
		return nil, errHalted
	}
	return &trackedConn{Conn: conn, p: p}, nil
}

// Blocked lists the hosts a computer was refused lately, most recent first.
func (p *Proxy) Blocked(computerID string) []Blocked {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.blocked[computerID])
	slices.Reverse(out)
	return out
}

// Forget drops what the proxy remembers about a computer, once it is gone.
func (p *Proxy) Forget(computerID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.blocked, computerID)
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
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request, generation uint64) {
	upstream, err := p.dialer.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		if errors.Is(err, netguard.ErrPrivateAddress) {
			p.denied(r.Context(), "non-public address")
		}
		refuse(w, dialStatus(err), err.Error())
		return
	}
	client, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upstream.Close()
		refuse(w, http.StatusInternalServerError, "the egress proxy could not take over the connection")
		return
	}
	if !p.trackGeneration(generation, client, upstream) {
		p.denied(r.Context(), "Eika is halted")
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
		head, _ := buffered.Peek(n)
		if _, err := upstream.Write(head); err != nil {
			return
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(upstream, client)
		closeWrite(upstream)
	})
	wg.Go(func() {
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
	})
	wg.Wait()
}

func (p *Proxy) currentGeneration() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

func (p *Proxy) validGeneration(generation uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tunnels != nil && !p.isHalted() && generation == p.generation
}

func (p *Proxy) track(conns ...net.Conn) bool {
	return p.trackGeneration(p.currentGeneration(), conns...)
}

func (p *Proxy) trackGeneration(generation uint64, conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tunnels == nil || p.isHalted() || generation != p.generation {
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

// block records that a computer was refused a host, and logs the first
// refusal of each host it remembers.
func (p *Proxy) block(computerID, host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := p.blocked[computerID]
	now := time.Now().UTC()
	i := slices.IndexFunc(list, func(b Blocked) bool { return b.Host == host })
	if i >= 0 {
		b := list[i]
		b.Count++
		b.Last = now
		list = append(slices.Delete(list, i, i+1), b)
	} else {
		p.log.Info("egress refused", "computer_id", computerID, "host", host)
		list = append(list, Blocked{Host: host, Count: 1, Last: now})
		if len(list) > maxBlocked {
			list = list[len(list)-maxBlocked:]
		}
	}
	p.blocked[computerID] = list
}

// credentials reads the computer id and token from Proxy-Authorization.
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
	_, _ = io.WriteString(w, "eika egress: "+msg+"\n")
}

// dialStatus is the status a failed connection is answered with: 403 for a
// destination the address check refused, 502 for one that could not be
// reached.
func dialStatus(err error) int {
	if errors.Is(err, netguard.ErrPrivateAddress) || errors.Is(err, errHalted) {
		return http.StatusForbidden
	}
	return http.StatusBadGateway
}

// closeWrite half-closes a connection, telling the far side no more bytes
// are coming while the other direction finishes.
func closeWrite(c net.Conn) {
	if tcp, ok := c.(interface{ CloseWrite() error }); ok {
		_ = tcp.CloseWrite()
		return
	}
	c.Close()
}
