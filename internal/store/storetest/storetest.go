package storetest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/jackc/pgx/v5"
)

// databaseEnv names the environment variable that points the tests at a
// PostgreSQL server instead of starting one.
const databaseEnv = "EIKA_TEST_DATABASE_URL"

// image is the container the tests start when they have to start one.
const image = "postgres:16"

// serverURL is the connection URL of the server the tests run against, and
// skipReason says why there is none. Main sets both before any test runs.
var (
	serverURL  string
	skipReason string
)

// Main runs a package's tests against PostgreSQL and returns the exit code
// the caller passes to os.Exit. It starts a container only when
// EIKA_TEST_DATABASE_URL is unset.
func Main(m *testing.M) int {
	if raw := os.Getenv(databaseEnv); raw != "" {
		serverURL = raw
		return m.Run()
	}
	stop, err := startPostgres()
	if err != nil {
		skipReason = err.Error()
		return m.Run()
	}
	defer stop()
	return m.Run()
}

// Open returns a store on a database of this test's own, dropped when the
// test ends. It skips the test when there is no database to talk to.
func Open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), URL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// URL creates a database for this test and returns its connection URL. Use it
// when a test opens a store more than once, such as one that checks that
// migrations run again cleanly.
func URL(t *testing.T) string {
	t.Helper()
	if skipReason != "" {
		t.Skip("no test database: " + skipReason)
	}
	name := "eika_test_" + strings.ToLower(store.NewID())
	admin, err := pgx.Connect(t.Context(), serverURL)
	if err != nil {
		t.Fatalf("connect to %s: %v", databaseEnv, err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(t.Context())) }()
	if _, err := admin.Exec(t.Context(), `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, serverURL)
		if err != nil {
			t.Logf("drop database %s: %v", name, err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
	})
	return databaseURL(t, name)
}

// databaseURL points the server URL at another database on the same server.
func databaseURL(t *testing.T, name string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse %s: %v", databaseEnv, err)
	}
	u.Path = "/" + name
	return u.String()
}

// startPostgres runs a throwaway PostgreSQL container and returns the
// function that removes it again.
func startPostgres() (func(), error) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		return nil, fmt.Errorf("docker is not available: %w", err)
	}
	out, err := exec.Command("docker", "run", "--detach",
		"--env", "POSTGRES_PASSWORD=eika",
		"--env", "POSTGRES_USER=eika",
		"--env", "POSTGRES_DB=eika",
		"--publish", "127.0.0.1::5432",
		image).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run %s: %v: %s", image, err, out)
	}
	id := strings.TrimSpace(string(out))
	remove := func() { _ = exec.Command("docker", "rm", "--force", "--volumes", id).Run() }

	port, err := exec.Command("docker", "port", id, "5432/tcp").Output()
	if err != nil {
		remove()
		return nil, fmt.Errorf("read published port: %w", err)
	}
	addr := strings.TrimSpace(strings.Split(string(port), "\n")[0])
	serverURL = fmt.Sprintf("postgres://eika:eika@%s/eika?sslmode=disable", addr)

	if err := waitReady(serverURL); err != nil {
		remove()
		return nil, err
	}
	return remove, nil
}

// waitReady blocks until the server accepts connections.
func waitReady(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var last error
	for {
		conn, err := pgx.Connect(ctx, url)
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s: %w", image, last)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
