//go:build docker

package computer_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
)

// sandboxImage is the image the Docker tests run computers from. It must
// carry git; build it with `make sandbox`.
const sandboxImage = "eika-sandbox:latest"

// requireDocker skips a test that has no Docker daemon to talk to.
func requireDocker(t *testing.T) string {
	t.Helper()
	socket := "/var/run/docker.sock"
	if _, err := os.Stat(socket); err != nil {
		t.Skip("no docker socket at " + socket)
	}
	return socket
}

// requireImage skips a test when the sandbox image has not been built.
func requireImage(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "image", "inspect", sandboxImage).Run(); err != nil {
		t.Skip(sandboxImage + " is missing: run `make sandbox`")
	}
}

// buildEikad builds the static daemon binary the host injects into
// containers.
func buildEikad(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eikad")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", path, "github.com/erlidev/eika/cmd/eikad")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build eikad: %v: %s", err, out)
	}
	return path
}

// cleanUp destroys a computer left over by a test. It cannot use the test's
// context: that is already cancelled by the time cleanup functions run.
func cleanUp(host *computer.Host, cx *computer.Computer) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Destroy(ctx, cx)
}

// run executes a shell command in the computer and returns its output.
func run(t *testing.T, ex executor.Executor, script string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	res, err := ex.Exec(t.Context(), executor.ExecSpec{
		Command: script,
		Shell:   true,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("run %q: %v", script, err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("run %q: exit %d: %s", script, res.ExitCode, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// newHost builds a host whose sandboxes publish their daemon port on
// 127.0.0.1, because the test runs outside the compose network.
func newHost(t *testing.T) *computer.Host {
	t.Helper()
	host, err := computer.NewHost(computer.Options{
		DockerSocket: requireDocker(t),
		Image:        sandboxImage,
		EikadBinary:  buildEikad(t),
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { host.Close() })
	return host
}

func TestComputerLifecycle(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	cx, err := host.Create(t.Context(), computer.Spec{
		Confinement: computer.Confinement{Limits: computer.Limits{CPUs: 1, MemoryBytes: 512 << 20, PIDs: 256}},
		Labels:      map[string]string{"eika.test": "lifecycle"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			cleanUp(host, &cx)
		}
	})
	if err := host.Start(t.Context(), &cx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if cx.State != computer.StateRunning || cx.Address == "" {
		t.Fatalf("computer = %+v, want a running computer with an address", cx)
	}

	ex, err := host.Executor(cx)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}

	t.Run("runs a command", func(t *testing.T) {
		if got := run(t, ex, "echo hi"); got != "hi" {
			t.Errorf("echo hi = %q, want hi", got)
		}
	})

	t.Run("runs unprivileged under an init", func(t *testing.T) {
		if got := run(t, ex, "id -u"); got == "0" {
			t.Error("the sandbox runs as root")
		}
		// Docker's init is PID 1, so the orphans a shell leaves are reaped.
		if got := run(t, ex, "cat /proc/1/comm"); strings.Contains(got, "eikad") {
			t.Errorf("PID 1 is %q, want docker's init", got)
		}
		if got := run(t, ex, "grep CapEff /proc/self/status"); !strings.HasSuffix(got, "0000000000000000") {
			t.Errorf("effective capabilities = %q, want none", got)
		}
	})

	t.Run("holds its processes to the limits", func(t *testing.T) {
		limits := run(t, ex, "cat /sys/fs/cgroup/cpu.max /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory.swap.max /sys/fs/cgroup/pids.max")
		if want := "100000 100000\n536870912\n0\n256"; limits != want {
			t.Errorf("cgroup limits = %q, want %q", limits, want)
		}
	})

	t.Run("reports what it uses", func(t *testing.T) {
		usage, err := host.Usage(t.Context(), cx)
		if err != nil {
			t.Fatalf("usage: %v", err)
		}
		if usage.MemoryBytes <= 0 || usage.MemoryLimitBytes != 512<<20 || usage.PIDs < 1 || usage.SampledAt.IsZero() {
			t.Errorf("usage = %+v, want memory under a 512 MiB limit and at least one process", usage)
		}
	})

	t.Run("changes its limits while it runs", func(t *testing.T) {
		if err := host.Confine(t.Context(), &cx, computer.Confinement{Limits: computer.Limits{CPUs: 0.5, MemoryBytes: 256 << 20}}); err != nil {
			t.Fatalf("confine: %v", err)
		}
		capacity, err := host.Capacity(t.Context())
		if err != nil {
			t.Fatalf("capacity: %v", err)
		}
		limits := run(t, ex, "cat /sys/fs/cgroup/cpu.max /sys/fs/cgroup/memory.max /sys/fs/cgroup/pids.max")
		if want := "50000 100000\n268435456\nmax"; limits != want {
			t.Errorf("cgroup limits = %q, want %q", limits, want)
		}
		// Docker cannot lift a limit from a container, so no limit is the
		// whole host.
		if err := host.Confine(t.Context(), &cx, computer.Confinement{}); err != nil {
			t.Fatalf("confine: %v", err)
		}
		limits = run(t, ex, "cat /sys/fs/cgroup/cpu.max /sys/fs/cgroup/memory.max")
		if want := fmt.Sprintf("%d 100000\n%d", capacity.CPUs*100000, capacity.MemoryBytes); limits != want {
			t.Errorf("cgroup limits = %q, want the host's %q", limits, want)
		}
	})

	t.Run("refuses a proxied network outside compose", func(t *testing.T) {
		if err := host.Confine(t.Context(), &cx, computer.Confinement{Proxied: true}); !errors.Is(err, computer.ErrNoEgressControl) {
			t.Errorf("confine = %v, want ErrNoEgressControl", err)
		}
	})

	t.Run("writes and reads a file", func(t *testing.T) {
		if err := ex.WriteFile(t.Context(), "notes/hello.txt", []byte("hei")); err != nil {
			t.Fatalf("write file: %v", err)
		}
		data, err := ex.ReadFile(t.Context(), "notes/hello.txt", executor.ReadOpts{})
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != "hei" {
			t.Errorf("read = %q, want hei", data)
		}
		if got := run(t, ex, "cat /home/eika/notes/hello.txt"); got != "hei" {
			t.Errorf("the file is not in the container: %q", got)
		}
	})

	t.Run("runs a long-lived process in the computer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		p, err := host.Process(ctx, cx, sandbox.ProcessSpec{Command: "sh", Args: []string{"-c", `read line; echo "$line $(cat notes/hello.txt)"`}}, nil)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		defer p.Close()
		if _, err := p.Write([]byte("got\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		out, err := io.ReadAll(p)
		// The process runs in the computer root and exits once it answers.
		if string(out) != "got hei\n" || err == nil || !strings.Contains(err.Error(), "status 0") {
			t.Errorf("output = %q, %v; want the answer, then the exit status", out, err)
		}
	})

	t.Run("lists and inspects the computer", func(t *testing.T) {
		listed, err := host.List(t.Context())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		found := false
		for _, w := range listed {
			if w.ID == cx.ID {
				found = true
				if w.Token != cx.Token || w.State != computer.StateRunning {
					t.Errorf("listed computer = %+v, want the running computer with its token", w)
				}
			}
		}
		if !found {
			t.Errorf("list did not report computer %s", cx.ID)
		}
	})

	t.Run("stops and destroys", func(t *testing.T) {
		if err := host.Stop(t.Context(), &cx); err != nil {
			t.Fatalf("stop: %v", err)
		}
		stopped, err := host.Inspect(t.Context(), cx.ID)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		if stopped.State != computer.StateStopped {
			t.Errorf("state = %q, want stopped", stopped.State)
		}
		// A harness that restarted knows the volume only through Inspect, and
		// Destroy removes only the volume it knows.
		if stopped.Volume != computer.VolumeName(cx.ID) {
			t.Errorf("inspected volume = %q, want %q", stopped.Volume, computer.VolumeName(cx.ID))
		}
		if err := host.Destroy(t.Context(), &cx); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		destroyed = true
		if _, err := host.Inspect(t.Context(), cx.ID); err == nil {
			t.Error("the container outlived Destroy")
		}
		if err := exec.Command("docker", "volume", "inspect", computer.VolumeName(cx.ID)).Run(); err == nil {
			t.Error("the volume outlived Destroy")
		}
	})
}

func TestFailedCreateLeavesNothingBehind(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host, err := computer.NewHost(computer.Options{
		DockerSocket: requireDocker(t),
		Image:        sandboxImage,
		// Injection fails, which is the last step of Create.
		EikadBinary: filepath.Join(t.TempDir(), "absent"),
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { host.Close() })

	id := "rollback" + fmt.Sprint(time.Now().UnixNano())
	if _, err := host.Create(t.Context(), computer.Spec{ID: id}); err == nil {
		t.Fatal("Create succeeded without an eikad binary")
	}
	if err := exec.Command("docker", "inspect", computer.ContainerName(id)).Run(); err == nil {
		t.Error("the container outlived a failed Create")
	}
	if err := exec.Command("docker", "volume", "inspect", computer.VolumeName(id)).Run(); err == nil {
		t.Error("the volume outlived a failed Create")
	}
}

func TestListReturnsAnInspectionFailure(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	id := "badlist" + fmt.Sprint(time.Now().UnixNano())
	containerName := "misnamed-" + id
	create := exec.Command("docker", "create",
		"--name", containerName,
		"--label", computer.Label+"="+id,
		sandboxImage,
	)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create mislabeled container: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", containerName).Run() })

	host, err := computer.NewHost(computer.Options{
		DockerSocket: requireDocker(t),
		Image:        sandboxImage,
		EikadBinary:  "/not-used",
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })
	if _, err := host.List(t.Context()); err == nil {
		t.Fatal("List dropped a container that it could not inspect")
	}
}

func TestComputerFromADockerfile(t *testing.T) {
	requireDocker(t)
	host := newHost(t)

	// An image that has never heard of eikad still works, because the daemon
	// is copied in before the container starts.
	build := t.TempDir()
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"),
		[]byte("FROM alpine:3.20\nRUN mkdir -p /home/eika && echo baked > /home/eika/from-the-image.txt\n"), 0o600); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}

	cx, err := host.Create(t.Context(), computer.Spec{BuildContext: build})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(host, &cx) })
	if err := host.Start(t.Context(), &cx); err != nil {
		t.Fatalf("start: %v", err)
	}
	ex, err := host.Executor(cx)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}

	// The volume at the root starts with what the image holds there.
	data, err := ex.ReadFile(t.Context(), "from-the-image.txt", executor.ReadOpts{})
	if err != nil {
		t.Fatalf("read the file baked into the image: %v", err)
	}
	if strings.TrimSpace(string(data)) != "baked" {
		t.Errorf("read = %q, want baked", data)
	}
	if got := run(t, ex, "echo built"); got != "built" {
		t.Errorf("echo = %q, want built", got)
	}

	// The image was built for this computer alone, so it goes with it.
	image := cx.Image
	if err := host.Destroy(t.Context(), &cx); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := exec.Command("docker", "image", "inspect", image).Run(); err == nil {
		t.Errorf("image %s outlived Destroy", image)
	}
}

func TestHasImageTellsPresentFromMissing(t *testing.T) {
	requireImage(t)
	host := newHost(t)
	if ok, err := host.HasImage(t.Context(), sandboxImage); err != nil || !ok {
		t.Errorf("HasImage(%s) = %v, %v; want present", sandboxImage, ok, err)
	}
	if ok, err := host.HasImage(t.Context(), "eika-test-absent-"+strings.ToLower(t.Name())+":never"); err != nil || ok {
		t.Errorf("HasImage of a missing image = %v, %v; want absent", ok, err)
	}
}

// testNetwork creates a Docker network for one test and removes it after.
func testNetwork(t *testing.T, internal bool) string {
	t.Helper()
	name := fmt.Sprintf("eika-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	args := []string{"network", "create"}
	if internal {
		args = append(args, "--internal")
	}
	if out, err := exec.Command("docker", append(args, name)...).CombinedOutput(); err != nil {
		t.Fatalf("create network: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "network", "rm", "-f", name).Run() })
	return name
}

// networksOf lists the networks a container is attached to.
func networksOf(t *testing.T, containerID string) []string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}", containerID).Output()
	if err != nil {
		t.Fatalf("inspect networks: %v", err)
	}
	return strings.Fields(string(out))
}

func TestConfineMovesAComputerBetweenTheSandboxNetworks(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	open, internal := testNetwork(t, false), testNetwork(t, true)
	host, err := computer.NewHost(computer.Options{
		DockerSocket:    requireDocker(t),
		Image:           sandboxImage,
		EikadBinary:     buildEikad(t),
		Network:         open,
		InternalNetwork: internal,
		EgressProxyURL:  "http://eika:3128",
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { host.Close() })
	if !host.EgressControl() {
		t.Fatal("a host with both networks has no egress control")
	}

	// The test runs outside both networks, so the container is never
	// started: joining and leaving networks works on a created one.
	cx, err := host.Create(t.Context(), computer.Spec{Confinement: computer.Confinement{Proxied: true}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(host, &cx) })
	if got := networksOf(t, cx.ContainerID); len(got) != 1 || got[0] != internal {
		t.Fatalf("a proxied computer is on %v, want only %s", got, internal)
	}
	inspected, err := host.Inspect(t.Context(), cx.ID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !inspected.Proxied {
		t.Error("Inspect does not report the computer proxied")
	}
	// The proxy knows the computer by a token of its own, which survives a
	// harness restart and is not the hub token.
	if inspected.ProxyToken == "" || inspected.ProxyToken != cx.ProxyToken {
		t.Errorf("Inspect's proxy token is not the computer's own")
	}

	if err := host.Confine(t.Context(), &cx, computer.Confinement{}); err != nil {
		t.Fatalf("confine: %v", err)
	}
	if got := networksOf(t, cx.ContainerID); len(got) != 1 || got[0] != open {
		t.Errorf("an open computer is on %v, want only %s", got, open)
	}
	if cx.Proxied {
		t.Error("the computer is still marked proxied")
	}
	// Doing it twice changes nothing.
	if err := host.Confine(t.Context(), &cx, computer.Confinement{}); err != nil {
		t.Fatalf("confine again: %v", err)
	}
	if err := host.Confine(t.Context(), &cx, computer.Confinement{Proxied: true}); err != nil {
		t.Fatalf("confine back: %v", err)
	}
	if got := networksOf(t, cx.ContainerID); len(got) != 1 || got[0] != internal {
		t.Errorf("the computer is on %v, want only %s", got, internal)
	}
}

// recreateNetwork removes a network and creates another of the same name, as
// `docker compose down` and `up` do to the sandbox networks.
func recreateNetwork(t *testing.T, name string) {
	t.Helper()
	if out, err := exec.Command("docker", "network", "rm", name).CombinedOutput(); err != nil {
		t.Fatalf("remove network: %v: %s", err, out)
	}
	if out, err := exec.Command("docker", "network", "create", name).CombinedOutput(); err != nil {
		t.Fatalf("create network: %v: %s", err, out)
	}
}

// running reports whether a container is running.
func running(t *testing.T, containerID string) bool {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerID).Output()
	if err != nil {
		t.Fatalf("inspect state: %v", err)
	}
	return strings.TrimSpace(string(out)) == "true"
}

func TestStartRejoinsARecreatedSandboxNetwork(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	open := testNetwork(t, false)
	host, err := computer.NewHost(computer.Options{
		DockerSocket: requireDocker(t),
		Image:        sandboxImage,
		EikadBinary:  buildEikad(t),
		Network:      open,
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { host.Close() })
	cx, err := host.Create(t.Context(), computer.Spec{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(host, &cx) })

	// A container records its network's id only once it has run, so run it
	// before the network goes away.
	for _, args := range [][]string{{"start", cx.ContainerID}, {"stop", cx.ContainerID}} {
		if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
			t.Fatalf("docker %s: %v: %s", args[0], err, out)
		}
	}
	recreateNetwork(t, open)
	if err := exec.Command("docker", "start", cx.ContainerID).Run(); err == nil {
		t.Fatal("a container on a recreated network started without repair; the test no longer reproduces the fault")
	}

	// The test runs outside the network, so the daemon is never reachable
	// and Start fails its readiness wait; the container must be running.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err = host.Start(ctx, &cx)
	if err != nil && strings.Contains(err.Error(), "not found") {
		t.Fatalf("start: %v", err)
	}
	if !running(t, cx.ContainerID) {
		t.Errorf("the computer is not running after Start: %v", err)
	}
	if got := networksOf(t, cx.ContainerID); len(got) != 1 || got[0] != open {
		t.Errorf("the computer is on %v, want only %s", got, open)
	}
}
