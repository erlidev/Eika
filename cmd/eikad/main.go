// Command eikad is the sandbox daemon. It runs inside every workspace
// container and is the only thing the harness talks to when an agent reads a
// file, writes a file, or runs a command.
//
// This is a placeholder: it serves /healthz so the harness and the compose
// stack can already probe it. Exec, PTY, file, and watch endpoints arrive in
// phase 2.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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
	listen := flag.String("listen", ":9090", "address to listen on")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return server.Serve(ctx, *listen, mux, log)
}
