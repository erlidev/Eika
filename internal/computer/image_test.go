package computer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDrainDockerStreamReportsTheDaemonsError(t *testing.T) {
	if err := drainDockerStream(strings.NewReader(`{"stream":"step 1"}` + "\n")); err != nil {
		t.Errorf("drain = %v, want nil", err)
	}
	err := drainDockerStream(strings.NewReader(`{"stream":"step 1"}` + "\n" + `{"error":"no such file"}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("drain = %v, want the daemon's error", err)
	}
}

// The sandbox image spells the computer root where the Go constant cannot be
// read: its working directory, the daemon's -root flag, and the home
// directory it renames the user's to. Nothing that a tool needs may live
// under that directory, since a volume mounted over it keeps the directory as
// it was at the volume's creation.
func TestSandboxImageAgreesWithTheComputerRoot(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "sandbox", "Dockerfile"))
	if err != nil {
		t.Fatalf("read the sandbox Dockerfile: %v", err)
	}
	dockerfile := string(raw)
	for _, want := range []string{
		"WORKDIR " + Root + "\n",
		`CMD ["-listen", ":7000", "-root", "` + Root + `"]`,
		"usermod -l eika -d " + Root + " -m",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("the sandbox Dockerfile has no %q", want)
		}
	}
	for _, line := range strings.Split(dockerfile, "\n") {
		code, _, _ := strings.Cut(line, "#")
		if !strings.HasPrefix(strings.TrimSpace(code), "ENV") {
			continue
		}
		if strings.Contains(code, Root) {
			t.Errorf("%q puts an environment variable under the home volume", strings.TrimSpace(line))
		}
	}
}
