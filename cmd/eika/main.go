// Command eika runs the Eika harness: the HTTP API, the event stream, and,
// when built together with the frontend, the web UI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "eika:", err)
		os.Exit(1)
	}
}

// run wires the harness together and serves until the process is signalled.
func run() error {
	configPath := flag.String("config", "", "path to the YAML configuration file")
	webDir := flag.String("web", "", "directory holding the built frontend; empty serves the API only")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	log.Info("configuration loaded", "config", cfg.String())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return server.New(cfg, log, server.Options{WebDir: *webDir}).Run(ctx)
}
