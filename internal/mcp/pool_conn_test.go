package mcp_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/mcp/mcptest"
)

// eventually fails the test unless cond holds within five seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// failingStore is a store whose server list cannot be read.
type failingStore struct{ memStore }

func (*failingStore) MCPServers(context.Context) ([]mcp.ServerConfig, error) {
	return nil, errors.New("database down")
}

func TestPoolStartConnectsTheEnabledRemoteServers(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	l := &launcher{server: s}
	store := &memStore{servers: []mcp.ServerConfig{
		{ID: "on", Name: "on", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true},
		{ID: "off", Name: "off", Kind: mcp.KindHTTP, URL: serve(t, s)},
		{ID: "local", Name: "local", Kind: mcp.KindStdio, Command: "npx", Enabled: true},
	}}
	pool := newPool(t, store, mcp.Options{Launcher: l})
	if err := pool.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eventually(t, "the enabled remote server to connect", func() bool {
		return pool.Status("on").State == mcp.StateConnected
	})
	if st := pool.Status("off"); st.State != mcp.StateDisabled {
		t.Errorf("status of the server turned off = %+v, want disabled", st)
	}
	if st := pool.Status("local"); st.State != mcp.StateIdle || len(l.pipes) != 0 {
		t.Errorf("stdio server status = %+v, launched %v; want it left alone", st, l.pipes)
	}

	failing := mcp.NewPool(mcp.Options{Store: &failingStore{}})
	defer failing.Close()
	if err := failing.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "list mcp servers: database down") {
		t.Errorf("Start with an unreadable store = %v", err)
	}
}

func TestPoolListsAgainWhenAListChanges(t *testing.T) {
	for _, era := range []string{mcptest.Modern, mcptest.Legacy} {
		t.Run(era, func(t *testing.T) {
			s := &mcptest.Server{Era: era, Tools: []mcptest.Tool{echoTool("echo")}}
			store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true}}}
			pool := newPool(t, store, mcp.Options{})
			pool.Tools(context.Background(), "")
			before, err := pool.Details(context.Background(), "s1")
			if err != nil {
				t.Fatal(err)
			}
			lists := func() int {
				n := 0
				for _, m := range s.Methods() {
					if m == "tools/list" {
						n++
					}
				}
				return n
			}
			listed := lists()
			// The stream opens asynchronously; announce until it is heard.
			eventually(t, "the tools to be listed again", func() bool {
				s.ListChanged()
				return lists() > listed
			})
			eventually(t, "the catalog to be replaced", func() bool {
				d, _ := pool.Details(context.Background(), "s1")
				return d.Catalog.FetchedAt.After(before.Catalog.FetchedAt) && len(d.Catalog.Tools) == 1
			})
		})
	}
}

// noisyPipe is a stdio server's streams that write one log notification
// before anything else.
type noisyPipe struct {
	io.ReadWriteCloser
	once sync.Once
}

func (p *noisyPipe) Read(b []byte) (int, error) {
	n := 0
	p.once.Do(func() {
		n = copy(b, `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"warning","logger":"db","data":"slow query"}}`+"\n")
	})
	if n > 0 {
		return n, nil
	}
	return p.ReadWriteCloser.Read(b)
}

// noisyLauncher starts stdio servers over noisy pipes.
type noisyLauncher struct{ server *mcptest.Server }

func (l noisyLauncher) Launch(context.Context, string, mcp.Command, func(string)) (io.ReadWriteCloser, error) {
	return &noisyPipe{ReadWriteCloser: l.server.Pipe()}, nil
}

func TestPoolKeepsTheServersLogMessages(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "local", Kind: mcp.KindStdio, Command: "npx", Enabled: true}}}
	pool := newPool(t, store, mcp.Options{Launcher: noisyLauncher{server: s}})
	if got := pool.Tools(context.Background(), "ws1"); len(got) != 1 {
		t.Fatalf("tools = %v", toolNames(got))
	}
	want := mcp.LogLine{Source: "server", Level: "warning", Text: "db: slow query"}
	eventually(t, "the log message", func() bool {
		d, _ := pool.Details(context.Background(), "s1")
		return slices.ContainsFunc(d.Logs, func(l mcp.LogLine) bool {
			return l.Source == want.Source && l.Level == want.Level && l.Text == want.Text
		})
	})
}

func TestPoolReportsAConnectionThatEndsByItself(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	l := &launcher{server: s}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "local", Kind: mcp.KindStdio, Command: "npx", Enabled: true}}}
	pool := newPool(t, store, mcp.Options{Launcher: l})
	if got := pool.Tools(context.Background(), "ws1"); len(got) != 1 {
		t.Fatalf("tools = %v", toolNames(got))
	}
	l.mu.Lock()
	process := l.pipes["ws1"][0]
	l.mu.Unlock()
	_ = process.Close()

	eventually(t, "the server to be reported in error", func() bool {
		return pool.Status("s1").State == mcp.StateError
	})
	d, _ := pool.Details(context.Background(), "s1")
	if !slices.ContainsFunc(d.Logs, func(l mcp.LogLine) bool {
		return l.Source == "eika" && l.Level == "error" && strings.HasPrefix(l.Text, "connection lost in workspace ws1: ")
	}) {
		t.Errorf("logs = %+v, want the lost connection", d.Logs)
	}
}
