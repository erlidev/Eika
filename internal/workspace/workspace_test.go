package workspace_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// testLogger returns a logger that discards everything.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testHub builds a hub over a temporary directory.
func testHub(t *testing.T) *hub.Hub {
	t.Helper()
	h, err := hub.New(t.TempDir(), testLogger())
	if err != nil {
		t.Fatalf("new hub: %v", err)
	}
	return h
}

func TestNewHostRejectsIncompleteOptions(t *testing.T) {
	complete := workspace.Options{
		DockerSocket: "/var/run/docker.sock",
		Hub:          testHub(t),
		Image:        "eika-sandbox:latest",
		EikadBinary:  "/usr/local/share/eika/eikad",
	}
	noHub := complete
	noHub.Hub = nil
	noImage := complete
	noImage.Image = ""
	noBinary := complete
	noBinary.EikadBinary = ""
	relativeProxy := complete
	relativeProxy.EgressProxyURL = "eika:3128"

	for name, opts := range map[string]workspace.Options{
		"no hub":               noHub,
		"no image":             noImage,
		"no binary":            noBinary,
		"a relative proxy url": relativeProxy,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := workspace.NewHost(opts, testLogger()); err == nil {
				t.Error("NewHost accepted incomplete options")
			}
		})
	}

	h, err := workspace.NewHost(complete, testLogger())
	if err != nil {
		t.Fatalf("NewHost rejected complete options: %v", err)
	}
	defer h.Close()
	if h.EgressControl() {
		t.Error("a host with no sandbox networks reports egress control")
	}

	// A workspace that is not running has no address to talk to.
	if _, err := h.Executor(workspace.Workspace{ID: "abc"}); err == nil {
		t.Error("Executor accepted a workspace with no address")
	}
	ex, err := h.Executor(workspace.Workspace{ID: "abc", Address: "http://eika-ws-abc:7000", Token: "t"})
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	if ex.Root() != workspace.Root {
		t.Errorf("Root() = %q, want %q", ex.Root(), workspace.Root)
	}
}

func TestNames(t *testing.T) {
	if got := workspace.ContainerName("abc"); got != "eika-ws-abc" {
		t.Errorf("ContainerName = %q, want eika-ws-abc", got)
	}
	if got := workspace.VolumeName("abc"); got != "eika-ws-abc" {
		t.Errorf("VolumeName = %q, want eika-ws-abc", got)
	}
}
