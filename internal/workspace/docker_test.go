//go:build docker

package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/workspace"
)

// sandboxImage is the image the Docker tests run workspaces from. It must
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

// cleanUp destroys a workspace left over by a test. It cannot use the test's
// context: that is already cancelled by the time cleanup functions run.
func cleanUp(host *workspace.Host, ws *workspace.Workspace) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host.Destroy(ctx, ws)
}

// run executes a shell command in the workspace and returns its output.
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
func newHost(t *testing.T) *workspace.Host {
	t.Helper()
	host, err := workspace.NewHost(workspace.Options{
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

func TestWorkspaceLifecycle(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	ws, err := host.Create(t.Context(), workspace.Spec{
		Confinement: workspace.Confinement{Limits: workspace.Limits{CPUs: 1, MemoryBytes: 512 << 20, PIDs: 256}},
		Labels:      map[string]string{"eika.test": "lifecycle"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			cleanUp(host, &ws)
		}
	})
	if err := host.Start(t.Context(), &ws); err != nil {
		t.Fatalf("start: %v", err)
	}
	if ws.State != workspace.StateRunning || ws.Address == "" {
		t.Fatalf("workspace = %+v, want a running workspace with an address", ws)
	}

	ex, err := host.Executor(ws)
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
		usage, err := host.Usage(t.Context(), ws)
		if err != nil {
			t.Fatalf("usage: %v", err)
		}
		if usage.MemoryBytes <= 0 || usage.MemoryLimitBytes != 512<<20 || usage.PIDs < 1 || usage.SampledAt.IsZero() {
			t.Errorf("usage = %+v, want memory under a 512 MiB limit and at least one process", usage)
		}
	})

	t.Run("changes its limits while it runs", func(t *testing.T) {
		if err := host.Confine(t.Context(), &ws, workspace.Confinement{Limits: workspace.Limits{CPUs: 0.5, MemoryBytes: 256 << 20}}); err != nil {
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
		if err := host.Confine(t.Context(), &ws, workspace.Confinement{}); err != nil {
			t.Fatalf("confine: %v", err)
		}
		limits = run(t, ex, "cat /sys/fs/cgroup/cpu.max /sys/fs/cgroup/memory.max")
		if want := fmt.Sprintf("%d 100000\n%d", capacity.CPUs*100000, capacity.MemoryBytes); limits != want {
			t.Errorf("cgroup limits = %q, want the host's %q", limits, want)
		}
	})

	t.Run("refuses a proxied network outside compose", func(t *testing.T) {
		if err := host.Confine(t.Context(), &ws, workspace.Confinement{Proxied: true}); !errors.Is(err, workspace.ErrNoEgressControl) {
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
		if got := run(t, ex, "cat /workspace/notes/hello.txt"); got != "hei" {
			t.Errorf("the file is not in the container: %q", got)
		}
	})

	t.Run("opens a terminal in the workspace", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		conn, err := host.Terminal(ctx, ws, 24, 80)
		if err != nil {
			t.Fatalf("terminal: %v", err)
		}
		defer conn.CloseNow()
		input, err := json.Marshal(eikad.PTYMessage{Type: eikad.PTYInput, Data: []byte("cat notes/hello.txt; exit 5\n")})
		if err != nil {
			t.Fatalf("encode input: %v", err)
		}
		if err := conn.Write(ctx, websocket.MessageText, input); err != nil {
			t.Fatalf("write input: %v", err)
		}
		var output strings.Builder
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v, output so far %q", err, output.String())
			}
			var msg eikad.PTYMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatalf("decode %q: %v", data, err)
			}
			if msg.Type == eikad.PTYOutput {
				output.Write(msg.Data)
				continue
			}
			if msg.Type != eikad.PTYExit || msg.ExitCode != 5 {
				t.Fatalf("final message = %+v, want exit 5", msg)
			}
			break
		}
		// The shell starts in the workspace root, where the file was written.
		if !strings.Contains(output.String(), "hei") {
			t.Errorf("output = %q, want the file's content", output.String())
		}
	})

	t.Run("runs a long-lived process in the workspace", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		p, err := host.Process(ctx, ws, sandbox.ProcessSpec{Command: "sh", Args: []string{"-c", `read line; echo "$line $(cat notes/hello.txt)"`}}, nil)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		defer p.Close()
		if _, err := p.Write([]byte("got\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		out, err := io.ReadAll(p)
		// The process runs in the workspace root and exits once it answers.
		if string(out) != "got hei\n" || err == nil || !strings.Contains(err.Error(), "status 0") {
			t.Errorf("output = %q, %v; want the answer, then the exit status", out, err)
		}
	})

	t.Run("lists and inspects the workspace", func(t *testing.T) {
		listed, err := host.List(t.Context())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		found := false
		for _, w := range listed {
			if w.ID == ws.ID {
				found = true
				if w.Token != ws.Token || w.State != workspace.StateRunning {
					t.Errorf("listed workspace = %+v, want the running workspace with its token", w)
				}
			}
		}
		if !found {
			t.Errorf("list did not report workspace %s", ws.ID)
		}
	})

	t.Run("stops and destroys", func(t *testing.T) {
		if err := host.Stop(t.Context(), &ws); err != nil {
			t.Fatalf("stop: %v", err)
		}
		stopped, err := host.Inspect(t.Context(), ws.ID)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		if stopped.State != workspace.StateStopped {
			t.Errorf("state = %q, want stopped", stopped.State)
		}
		if err := host.Destroy(t.Context(), &ws); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		destroyed = true
		if _, err := host.Inspect(t.Context(), ws.ID); err == nil {
			t.Error("the container outlived Destroy")
		}
		if err := exec.Command("docker", "volume", "inspect", workspace.VolumeName(ws.ID)).Run(); err == nil {
			t.Error("the volume outlived Destroy")
		}
	})
}

func TestFailedCreateLeavesNothingBehind(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host, err := workspace.NewHost(workspace.Options{
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
	if _, err := host.Create(t.Context(), workspace.Spec{ID: id}); err == nil {
		t.Fatal("Create succeeded without an eikad binary")
	}
	if err := exec.Command("docker", "inspect", workspace.ContainerName(id)).Run(); err == nil {
		t.Error("the container outlived a failed Create")
	}
	if err := exec.Command("docker", "volume", "inspect", workspace.VolumeName(id)).Run(); err == nil {
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
		"--label", workspace.Label+"="+id,
		sandboxImage,
	)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create mislabeled container: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", containerName).Run() })

	host, err := workspace.NewHost(workspace.Options{
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

func TestWorkspaceFromADockerfileAndAHostPath(t *testing.T) {
	requireDocker(t)
	host := newHost(t)

	// An image that has never heard of eikad still works, because the daemon
	// is copied in before the container starts.
	build := t.TempDir()
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"),
		[]byte("FROM alpine:3.20\nRUN mkdir -p /workspace\n"), 0o600); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	hostPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostPath, "from-the-host.txt"), []byte("mounted"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ws, err := host.Create(t.Context(), workspace.Spec{BuildContext: build, HostPath: hostPath})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(host, &ws) })
	if err := host.Start(t.Context(), &ws); err != nil {
		t.Fatalf("start: %v", err)
	}
	ex, err := host.Executor(ws)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}

	data, err := ex.ReadFile(t.Context(), "from-the-host.txt", executor.ReadOpts{})
	if err != nil {
		t.Fatalf("read the bind-mounted file: %v", err)
	}
	if string(data) != "mounted" {
		t.Errorf("read = %q, want mounted", data)
	}
	if got := run(t, ex, "echo built"); got != "built" {
		t.Errorf("echo = %q, want built", got)
	}

	// The workspace writes back to the host directory.
	if err := ex.WriteFile(t.Context(), "written-back.txt", []byte("ok")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(hostPath, "written-back.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the workspace's write did not reach the host directory")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The image was built for this workspace alone, so it goes with it.
	image := ws.Image
	if err := host.Destroy(t.Context(), &ws); err != nil {
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

func TestConfineMovesAWorkspaceBetweenTheSandboxNetworks(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	open, internal := testNetwork(t, false), testNetwork(t, true)
	host, err := workspace.NewHost(workspace.Options{
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
	ws, err := host.Create(t.Context(), workspace.Spec{Confinement: workspace.Confinement{Proxied: true}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(host, &ws) })
	if got := networksOf(t, ws.ContainerID); len(got) != 1 || got[0] != internal {
		t.Fatalf("a proxied workspace is on %v, want only %s", got, internal)
	}
	inspected, err := host.Inspect(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !inspected.Proxied {
		t.Error("Inspect does not report the workspace proxied")
	}
	// The proxy knows the workspace by a token of its own, which survives a
	// harness restart and is not the hub token.
	if inspected.ProxyToken == "" || inspected.ProxyToken != ws.ProxyToken {
		t.Errorf("Inspect's proxy token is not the workspace's own")
	}

	if err := host.Confine(t.Context(), &ws, workspace.Confinement{}); err != nil {
		t.Fatalf("confine: %v", err)
	}
	if got := networksOf(t, ws.ContainerID); len(got) != 1 || got[0] != open {
		t.Errorf("an open workspace is on %v, want only %s", got, open)
	}
	if ws.Proxied {
		t.Error("the workspace is still marked proxied")
	}
	// Doing it twice changes nothing.
	if err := host.Confine(t.Context(), &ws, workspace.Confinement{}); err != nil {
		t.Fatalf("confine again: %v", err)
	}
	if err := host.Confine(t.Context(), &ws, workspace.Confinement{Proxied: true}); err != nil {
		t.Fatalf("confine back: %v", err)
	}
	if got := networksOf(t, ws.ContainerID); len(got) != 1 || got[0] != internal {
		t.Errorf("the workspace is on %v, want only %s", got, internal)
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
	host, err := workspace.NewHost(workspace.Options{
		DockerSocket: requireDocker(t),
		Image:        sandboxImage,
		EikadBinary:  buildEikad(t),
		Network:      open,
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { host.Close() })
	ws, err := host.Create(t.Context(), workspace.Spec{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(host, &ws) })

	// A container records its network's id only once it has run, so run it
	// before the network goes away.
	for _, args := range [][]string{{"start", ws.ContainerID}, {"stop", ws.ContainerID}} {
		if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
			t.Fatalf("docker %s: %v: %s", args[0], err, out)
		}
	}
	recreateNetwork(t, open)
	if err := exec.Command("docker", "start", ws.ContainerID).Run(); err == nil {
		t.Fatal("a container on a recreated network started without repair; the test no longer reproduces the fault")
	}

	// The test runs outside the network, so the daemon is never reachable
	// and Start fails its readiness wait; the container must be running.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err = host.Start(ctx, &ws)
	if err != nil && strings.Contains(err.Error(), "not found") {
		t.Fatalf("start: %v", err)
	}
	if !running(t, ws.ContainerID) {
		t.Errorf("the workspace is not running after Start: %v", err)
	}
	if got := networksOf(t, ws.ContainerID); len(got) != 1 || got[0] != open {
		t.Errorf("the workspace is on %v, want only %s", got, open)
	}
}
