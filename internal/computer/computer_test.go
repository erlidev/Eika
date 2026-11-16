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
}
