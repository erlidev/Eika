//go:build docker

package server_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

// realSandboxImage is the image the tests that use a real Docker daemon run
// the home computer from: the one `make check` builds for the Docker tests,
// named in EIKA_TEST_SANDBOX_IMAGE.
func realSandboxImage() string {
	if image := os.Getenv("EIKA_TEST_SANDBOX_IMAGE"); image != "" {
		return image
	}
	return "eika-sandbox:latest"
}

// realHome returns a server over a real Docker daemon, the real host behind
// it, and its store, skipping the test when there is no daemon or no sandbox
// image. The database is the test's own, so the home computer the server makes
// is the only one, and it is removed when the test ends.
func realHome(t *testing.T) (*server.Server, *computer.Host, *store.Store) {
	t.Helper()
	const socket = "/var/run/docker.sock"
	if _, err := os.Stat(socket); err != nil {
		t.Skip("no docker socket at " + socket)
	}
	image := realSandboxImage()
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skip(image + " is missing: run `make sandbox-test`")
	}
	eikad := filepath.Join(t.TempDir(), "eikad")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", eikad, "github.com/erlidev/eika/cmd/eikad")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build eikad: %v: %s", err, out)
	}
	host, err := computer.NewHost(computer.Options{DockerSocket: socket, Image: image, EikadBinary: eikad}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	cfg := testConfig()
	cfg.SandboxImage = image
	st := storetest.Open(t)
	s := server.New(cfg, testLogger(), server.Deps{Store: st, Computers: host}, server.Options{})
	t.Cleanup(s.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Minute)
		defer cancel()
		home, err := st.HomeComputer(ctx)
		if err != nil {
			return
		}
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", computer.ContainerName(home.ID)).Run()
		_ = exec.CommandContext(ctx, "docker", "volume", "rm", "-f", computer.VolumeName(home.ID)).Run()
	})
	return s, host, st
}

// cgroupLimits reads the limits the kernel holds a running computer to:
// CPU, memory, and process count.
func cgroupLimits(t *testing.T, host *computer.Host, cx computer.Computer) string {
	t.Helper()
	ex, err := host.Executor(cx)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	var stdout, stderr bytes.Buffer
	res, err := ex.Exec(t.Context(), executor.ExecSpec{
		Command: "cat /sys/fs/cgroup/cpu.max /sys/fs/cgroup/memory.max /sys/fs/cgroup/pids.max",
		Shell:   true, Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("read cgroup limits: %v, exit %d: %s", err, res.ExitCode, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// configuredLimits reads the limits a container is configured with, which a
// stopped container has as well as a running one.
func configuredLimits(t *testing.T, containerID string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f",
		"{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}}", containerID).Output()
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestSavingLimitsReachesTheRealHomeContainer(t *testing.T) {
	s, host, st := realHome(t)
	if err := s.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	row, err := st.HomeComputer(t.Context())
	if err != nil {
		t.Fatalf("find the home computer: %v", err)
	}
	cx, err := host.Inspect(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}

	save := func(cpus float64, memoryMB, pids int64) {
		t.Helper()
		rec := requestWith(t, s, "PUT", "/api/settings", settingsSandbox(cpus, memoryMB, pids, "open", nil), "Bearer "+testToken)
		decodeBody[settingsWire](t, rec, 200)
	}

	t.Run("a running container takes them at once", func(t *testing.T) {
		save(0.5, 300, 123)
		if got, want := cgroupLimits(t, host, cx), "50000 100000\n314572800\n123"; got != want {
			t.Errorf("cgroup limits = %q, want %q", got, want)
		}
	})

	t.Run("a stopped container takes them and keeps them across the start", func(t *testing.T) {
		if err := host.Stop(t.Context(), &cx); err != nil {
			t.Fatalf("stop: %v", err)
		}
		save(1, 256, 77)
		if got, want := configuredLimits(t, cx.ContainerID), "1000000000 268435456 77"; got != want {
			t.Errorf("configured limits of the stopped container = %q, want %q", got, want)
		}
		if err := s.EnsureHome(t.Context()); err != nil {
			t.Fatalf("EnsureHome with the container stopped: %v", err)
		}
		started, err := host.Inspect(t.Context(), row.ID)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		if got, want := cgroupLimits(t, host, started), "100000 100000\n268435456\n77"; got != want {
			t.Errorf("cgroup limits after the start = %q, want %q", got, want)
		}
	})
}
