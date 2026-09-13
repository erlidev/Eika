//go:build docker

package workspace_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
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

// serveHub serves the hub on every interface and returns its port, so that a
// sandbox container can reach it through the Docker bridge.
func serveHub(t *testing.T, h *hub.Hub) int {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h.Handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// containerGateway asks the container for the address of its default route,
// which is how it reaches a server running on the host.
func containerGateway(t *testing.T, ex executor.Executor) string {
	t.Helper()
	var stdout bytes.Buffer
	if _, err := ex.Exec(t.Context(), executor.ExecSpec{
		Command: "cat /proc/net/route",
		Shell:   true,
		Stdout:  &stdout,
	}); err != nil {
		t.Fatalf("read routes: %v", err)
	}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, binary.BigEndian.Uint32(raw))
		return ip.String()
	}
	t.Fatalf("no default route in %q", stdout.String())
	return ""
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
func newHost(t *testing.T, h *hub.Hub, hubURL string) *workspace.Host {
	t.Helper()
	host, err := workspace.NewHost(workspace.Options{
		DockerSocket: requireDocker(t),
		Hub:          h,
		HubURL:       hubURL,
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
	host := newHost(t, testHub(t), "")

	ws, err := host.Create(t.Context(), workspace.Spec{
		Limits: workspace.Limits{CPUs: 1, MemoryBytes: 512 << 20, PIDs: 256},
		Labels: map[string]string{"eika.test": "lifecycle"},
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

func TestWorkspaceClonesFromTheHubAndPushesBack(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	h := testHub(t)
	port := serveHub(t, h)

	// The hub URL is only known once a container can say how it reaches the
	// host, so the workspace is created against a host without one first.
	probe := newHost(t, h, "")
	ws, err := probe.Create(t.Context(), workspace.Spec{Project: "demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { cleanUp(probe, &ws) })
	if err := probe.Start(t.Context(), &ws); err != nil {
		t.Fatalf("start: %v", err)
	}
	ex, err := probe.Executor(ws)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	host := newHost(t, h, fmt.Sprintf("http://%s:%d", containerGateway(t, ex), port))

	base, err := host.Clone(t.Context(), ws, "demo", "work")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if base != "" {
		t.Errorf("base commit = %q, want empty for a new project", base)
	}
	// The token reaches git through the environment, never the remote URL.
	if remote := run(t, ex, "git remote get-url origin"); strings.Contains(remote, ws.HubToken) {
		t.Errorf("the remote url carries the hub token: %q", remote)
	}

	run(t, ex, "echo cloned > file.txt && git add file.txt && git commit -m 'from the workspace'")
	run(t, ex, "git push origin HEAD:refs/heads/work")

	path, err := h.Path("demo")
	if err != nil {
		t.Fatalf("hub path: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), "git", "-C", path, "log", "-1", "--format=%s", "work").Output()
	if err != nil {
		t.Fatalf("read the hub log: %v", err)
	}
	if strings.TrimSpace(string(out)) != "from the workspace" {
		t.Errorf("hub log = %q, want the workspace's commit", out)
	}

	hubStatus := func(project string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			fmt.Sprintf("http://127.0.0.1:%d/git/%s.git/info/refs?service=git-upload-pack", port, project), nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.SetBasicAuth(ws.ID, ws.HubToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("cannot reach another project", func(t *testing.T) {
		if _, err := h.Init(t.Context(), "other"); err != nil {
			t.Fatalf("init other: %v", err)
		}
		if code := hubStatus("other"); code != http.StatusForbidden {
			t.Errorf("other project status = %d, want 403", code)
		}
	})

	t.Run("rejects a branch name that is a command", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "pwned")
		_, err := host.Clone(t.Context(), ws, "demo", "main; touch "+marker)
		if !errors.Is(err, workspace.ErrBadBranch) {
			t.Errorf("Clone error = %v, want ErrBadBranch", err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Error("the branch name ran as a command")
		}
	})

	t.Run("loses hub access when it stops", func(t *testing.T) {
		if err := host.Stop(t.Context(), &ws); err != nil {
			t.Fatalf("stop: %v", err)
		}
		if _, err := host.Inspect(t.Context(), ws.ID); err != nil {
			t.Fatalf("inspect: %v", err)
		}
		if code := hubStatus("demo"); code != http.StatusUnauthorized {
			t.Errorf("stopped workspace status = %d, want 401", code)
		}
	})
}

func TestFailedCreateLeavesNothingBehind(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	h := testHub(t)
	host, err := workspace.NewHost(workspace.Options{
		DockerSocket: requireDocker(t),
		Hub:          h,
		Image:        sandboxImage,
		// Injection fails, which is the last step of Create.
		EikadBinary: filepath.Join(t.TempDir(), "absent"),
	}, testLogger())
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(func() { host.Close() })

	id := "rollback" + fmt.Sprint(time.Now().UnixNano())
	if _, err := host.Create(t.Context(), workspace.Spec{ID: id, Project: "demo"}); err == nil {
		t.Fatal("Create succeeded without an eikad binary")
	}
	if err := exec.Command("docker", "inspect", workspace.ContainerName(id)).Run(); err == nil {
		t.Error("the container outlived a failed Create")
	}
	if err := exec.Command("docker", "volume", "inspect", workspace.VolumeName(id)).Run(); err == nil {
		t.Error("the volume outlived a failed Create")
	}
}

func TestWorkspaceFromADockerfileAndAHostPath(t *testing.T) {
	requireDocker(t)
	h := testHub(t)
	host := newHost(t, h, "")

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
