//go:build docker

package computer_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/executor"
)

// removeContainer removes a computer's container and leaves its volume, which
// is all that is left of a computer whose container was lost.
func removeContainer(t *testing.T, cx computer.Computer) {
	t.Helper()
	if out, err := exec.Command("docker", "rm", "-f", cx.ContainerID).CombinedOutput(); err != nil {
		t.Fatalf("remove container: %v: %s", err, out)
	}
}

// volumeExists reports whether the Docker daemon has a volume.
func volumeExists(name string) bool {
	return exec.Command("docker", "volume", "inspect", name).Run() == nil
}

// startComputer creates and starts a computer, and removes it when the test
// ends. Pass the id of an earlier computer to make it again over its volume.
func startComputer(t *testing.T, host *computer.Host, spec computer.Spec) (computer.Computer, executor.Executor) {
	t.Helper()
	cx, err := host.Create(t.Context(), spec)
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
	return cx, ex
}

func TestFilesSurviveTheContainerBeingMadeAgain(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	first, ex := startComputer(t, host, computer.Spec{})
	if err := ex.WriteFile(t.Context(), "notes/kept.txt", []byte("still here")); err != nil {
		t.Fatalf("write: %v", err)
	}
	removeContainer(t, first)

	// Making the computer again under its id mounts the volume it had.
	second, ex := startComputer(t, host, computer.Spec{ID: first.ID})
	if second.ContainerID == first.ContainerID {
		t.Fatalf("the container was not made again")
	}
	data, err := ex.ReadFile(t.Context(), "notes/kept.txt", executor.ReadOpts{})
	if err != nil || string(data) != "still here" {
		t.Errorf("read after the container was made again = %q, %v; want the file written before", data, err)
	}
	if got := run(t, ex, "stat -c %u:%g notes/kept.txt"); got != "1000:1000" {
		t.Errorf("the file belongs to %s, want 1000:1000", got)
	}
}

func TestFilesSurviveAStopAndAStart(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	cx, ex := startComputer(t, host, computer.Spec{})
	if err := ex.WriteFile(t.Context(), "kept.txt", []byte("still here")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := host.Stop(t.Context(), &cx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := host.Start(t.Context(), &cx); err != nil {
		t.Fatalf("start: %v", err)
	}
	ex, err := host.Executor(cx)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	if got := run(t, ex, "cat kept.txt"); got != "still here" {
		t.Errorf("file after a restart = %q, want the one written before", got)
	}
}

func TestACreateThatFailsLeavesTheVolumeItFound(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	first, ex := startComputer(t, host, computer.Spec{})
	if err := ex.WriteFile(t.Context(), "kept.txt", []byte("still here")); err != nil {
		t.Fatalf("write: %v", err)
	}
	removeContainer(t, first)

	// The image the harness is told to use is not there, which is how making
	// the home computer again fails on a machine whose sandbox image was
	// removed. The files must not go with the attempt.
	_, err := host.Create(t.Context(), computer.Spec{ID: first.ID, Image: "eika-test-absent-" + strings.ToLower(t.Name()) + ":never"})
	if err == nil {
		t.Fatal("create from an image that does not exist succeeded")
	}
	if !volumeExists(first.Volume) {
		t.Fatalf("volume %s was removed by a create that failed", first.Volume)
	}

	_, ex = startComputer(t, host, computer.Spec{ID: first.ID})
	if got := run(t, ex, "cat kept.txt"); got != "still here" {
		t.Errorf("file after the failed create = %q, want the one written before", got)
	}
}

func TestACreateThatFailsRemovesAVolumeItMade(t *testing.T) {
	requireDocker(t)
	host := newHost(t)

	id := "eika-test-" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	if _, err := host.Create(t.Context(), computer.Spec{ID: id, Image: "eika-test-absent-" + id + ":never"}); err == nil {
		t.Fatal("create from an image that does not exist succeeded")
	}
	if volumeExists(computer.VolumeName(id)) {
		t.Errorf("volume %s outlived the create that made it and failed", computer.VolumeName(id))
	}
}

func TestTheSandboxImageKeepsItsToolsOutsideTheHomeVolume(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)
	_, ex := startComputer(t, host, computer.Spec{})

	// A fresh volume copies the image's /home/eika with its owner.
	if got := run(t, ex, "stat -c %u:%g /home/eika"); got != "1000:1000" {
		t.Errorf("/home/eika belongs to %s, want 1000:1000", got)
	}
	if got := run(t, ex, "echo $HOME; pwd"); got != "/home/eika\n/home/eika" {
		t.Errorf("HOME and the working directory = %q, want /home/eika for both", got)
	}
	// What the image installs for a tool lives under /opt, so the volume
	// freezing /home/eika at its first creation freezes nothing a tool needs.
	if got := run(t, ex, "go env GOPATH"); got != "/opt/go" {
		t.Errorf("GOPATH = %q, want /opt/go", got)
	}
	if got := run(t, ex, "echo $PATH"); !strings.Contains(got, "/opt/go/bin") || strings.Contains(got, "/home/eika") {
		t.Errorf("PATH = %q, want /opt/go/bin on it and nothing under /home/eika", got)
	}
	// `go install` writes the binary to $GOPATH/bin and the sources to
	// $GOPATH/pkg/mod, as uid 1000.
	if got := run(t, ex, "mkdir -p /opt/go/bin /opt/go/pkg/mod && touch /opt/go/bin/probe /opt/go/pkg/mod/probe && echo ok"); got != "ok" {
		t.Errorf("the eika user cannot write under /opt/go: %q", got)
	}
	if got := run(t, ex, "ls -A /home/eika | grep -c '^go$' || true"); got != "0" {
		t.Errorf("the image left a go directory in the home volume")
	}
}

func TestACustomImageGetsAWritableRootAndHome(t *testing.T) {
	requireDocker(t)
	host := newHost(t)

	cases := []struct {
		name       string
		dockerfile string
	}{
		{"an image with no /home/eika", "FROM alpine:3.20\n"},
		{"an image whose /home/eika belongs to root", "FROM alpine:3.20\nRUN mkdir -p /home/eika && echo baked > /home/eika/from-the-image.txt\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			build := t.TempDir()
			if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte(c.dockerfile), 0o600); err != nil {
				t.Fatalf("write dockerfile: %v", err)
			}
			_, ex := startComputer(t, host, computer.Spec{BuildContext: build})

			// alpine has no user for uid 1000, so the container runs as a
			// bare number: the root is still the user's home and its own.
			if got := run(t, ex, "id -u; echo $HOME; stat -c %u:%g /home/eika"); got != "1000\n/home/eika\n1000:1000" {
				t.Errorf("uid, HOME and the root's owner = %q", got)
			}
			if err := ex.WriteFile(t.Context(), "notes/new.txt", []byte("hei")); err != nil {
				t.Fatalf("write into the root of a custom image: %v", err)
			}
			if got := run(t, ex, "echo $(cat /home/eika/notes/new.txt) && touch /home/eika/x && echo writable"); got != "hei\nwritable" {
				t.Errorf("the shell cannot write to the root: %q", got)
			}
			if strings.Contains(c.dockerfile, "baked") {
				if data, err := ex.ReadFile(t.Context(), "from-the-image.txt", executor.ReadOpts{}); err != nil || strings.TrimSpace(string(data)) != "baked" {
					t.Errorf("what the image held at the root = %q, %v; want it kept", data, err)
				}
			}
		})
	}
}

func TestAnImageWithTheUserNamedInsteadIsLeftToItself(t *testing.T) {
	requireDocker(t)
	host := newHost(t)

	build := t.TempDir()
	dockerfile := "FROM alpine:3.20\nRUN adduser -D -u 2000 worker && mkdir -p /home/eika && chown worker /home/eika\n"
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	_, ex := startComputer(t, host, computer.Spec{BuildContext: build, User: "worker"})
	if got := run(t, ex, "id -un; stat -c %U /home/eika && touch /home/eika/x && echo writable"); got != "worker\nworker\nwritable" {
		t.Errorf("user, owner and write = %q", got)
	}
}
