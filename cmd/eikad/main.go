// Command eikad is the sandbox daemon. It runs inside every workspace
// container and is the only thing the harness talks to when an agent reads a
// file, writes a file, runs a command, opens a terminal, or watches for
// changes.
//
// It refuses to start without EIKAD_TOKEN, which the harness generates per
// workspace, so a sandbox is never reachable without authentication.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eikad:", err)
		os.Exit(1)
	}
}

// run serves the daemon until the container stops it.
func run() error {
	listen := flag.String("listen", ":7000", "address to listen on")
	root := flag.String("root", "/workspace", "workspace directory every path is confined to")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)

	token := os.Getenv(eikad.TokenEnv)
	if token == "" {
		return errors.New(eikad.TokenEnv + " is not set")
	}
	d, err := eikad.New(eikad.Options{Root: *root, Token: token}, log)
	if err != nil {
		return err
	}
	log.Info("sandbox daemon starting", "root", d.Root())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return server.Serve(ctx, *listen, d.Handler(), log)
}
