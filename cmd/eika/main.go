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
	reindex := flag.Bool("memory-reindex", false, "rebuild the memory index from the memory files, then exit")
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

	if *reindex {
		rep, err := server.ReindexMemory(ctx, cfg, log)
		if err != nil {
			return err
		}
		fmt.Printf("memory reindexed: %d rows written, %d committed, %d restored, %d removed, %d held pending, %d invalid\n",
			rep.Reindexed, rep.Committed, rep.Restored, rep.Removed, rep.Held, len(rep.Invalid))
		return nil
	}
	return server.Run(ctx, cfg, log, server.Options{WebDir: *webDir})
}
