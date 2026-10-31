package computer_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/erlidev/eika/internal/computer"
)

// testLogger returns a logger that discards everything.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewHostRejectsIncompleteOptions(t *testing.T) {
	complete := computer.Options{
		DockerSocket: "/var/run/docker.sock",
		Image:        "eika-sandbox:latest",
		EikadBinary:  "/usr/local/share/eika/eikad",
	}
	noImage := complete
	noImage.Image = ""
	noBinary := complete
	noBinary.EikadBinary = ""
	relativeProxy := complete
	relativeProxy.EgressProxyURL = "eika:3128"

	for name, opts := range map[string]computer.Options{
		"no image":             noImage,
		"no binary":            noBinary,
		"a relative proxy url": relativeProxy,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := computer.NewHost(opts, testLogger()); err == nil {
				t.Error("NewHost accepted incomplete options")
			}
		})
	}

	h, err := computer.NewHost(complete, testLogger())
	if err != nil {
		t.Fatalf("NewHost rejected complete options: %v", err)
	}
	defer h.Close()
	if h.EgressControl() {
		t.Error("a host with no sandbox networks reports egress control")
	}

	// A computer that is not running has no address to talk to.
	if _, err := h.Executor(computer.Computer{ID: "abc"}); err == nil {
		t.Error("Executor accepted a computer with no address")
	}
	ex, err := h.Executor(computer.Computer{ID: "abc", Address: "http://eika-cx-abc:7000", Token: "t"})
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	if ex.Root() != computer.Root {
		t.Errorf("Root() = %q, want %q", ex.Root(), computer.Root)
	}
}

func TestNames(t *testing.T) {
	if got := computer.ContainerName("abc"); got != "eika-cx-abc" {
		t.Errorf("ContainerName = %q, want eika-cx-abc", got)
	}
	if got := computer.VolumeName("abc"); got != "eika-cx-abc" {
		t.Errorf("VolumeName = %q, want eika-cx-abc", got)
	}
}
