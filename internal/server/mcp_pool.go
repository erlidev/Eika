package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace"
)

// mcpLauncher starts stdio MCP servers in workspaces through the host's
// /process route: a stdio server is a process an agent's session asked
// for, so it runs where the agent's processes do.
type mcpLauncher struct {
	store      *store.Store
	workspaces Workspaces
}

// Launch starts cmd in a running workspace, in the directory its files are
// in: a worktree workspace's server runs in its worktree, in the holder's
// container.
func (l mcpLauncher) Launch(ctx context.Context, workspaceID string, cmd mcp.Command, stderr func(string)) (io.ReadWriteCloser, error) {
	row, err := l.store.Workspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Locate(ctx, l.workspaces, row.ID, row.WorktreeOf)
	if err != nil {
		return nil, err
	}
	if ws.State != workspace.StateRunning {
		return nil, notRunning(row, ws.State)
	}
	return l.workspaces.Process(ctx, ws, sandbox.ProcessSpec{Command: cmd.Name, Args: cmd.Args, Env: cmd.Env}, stderr)
}

// mcpClient is how Eika introduces itself to an MCP server: by name, and
// by the version the build recorded.
func mcpClient() mcp.Implementation {
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	return mcp.Implementation{Name: "eika", Title: "Eika", Version: version}
}

// NewMCP builds the MCP pool over the servers st holds, their secrets
// sealed by secrets, starting stdio servers in workspaces through host and
// announcing every change on bus. The wiring passes the real host; a test
// passes its fake. The caller starts the pool and closes it after the
// server that uses it.
func NewMCP(cfg config.Config, st *store.Store, secrets *secret.Box, host Workspaces, bus event.Emitter, log *slog.Logger) *mcp.Pool {
	backend := mcpStore{store: st, secrets: secrets, log: log}
	opts := mcp.Options{
		Store:   backend,
		Emitter: bus,
		// No overall timeout: an event stream stays open as long as its
		// connection. Every request carries a deadline of its own.
		HTTPClient: &http.Client{},
		Client:     mcpClient(),
		PublicURL:  cfg.PublicURL,
		Logger:     log,
	}
	if host != nil {
		opts.Launcher = mcpLauncher{store: st, workspaces: host}
	}
	return mcp.NewPool(opts)
}
