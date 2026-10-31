package computer

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarEntries reads an archive into a map of name to contents.
func tarEntries(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	out := make(map[string]string)
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %s: %v", header.Name, err)
		}
		out[header.Name] = string(data)
	}
}

func TestTarFileCarriesTheExecutableBit(t *testing.T) {
	archive, err := tarFile("eikad", []byte("binary"), 0o755)
	if err != nil {
		t.Fatalf("tar file: %v", err)
	}
	tr := tar.NewReader(archive)
	header, err := tr.Next()
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if header.Name != "eikad" || header.Mode != 0o755 || header.Size != 6 {
		t.Errorf("header = %+v, want an executable eikad of 6 bytes", header)
	}
}

func TestTarDirPacksTheBuildContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("make sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0o600); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "setup.sh"), []byte("echo hei"), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}

	archive, err := tarDir(dir)
	if err != nil {
		t.Fatalf("tar dir: %v", err)
	}
	entries := tarEntries(t, archive)
	if entries["Dockerfile"] != "FROM scratch" {
		t.Errorf("Dockerfile = %q, want its contents", entries["Dockerfile"])
	}
	if entries["sub/setup.sh"] != "echo hei" {
		t.Errorf("sub/setup.sh = %q, want its contents", entries["sub/setup.sh"])
	}
	if _, ok := entries["sub/"]; !ok {
		t.Errorf("entries = %v, want the sub directory", entries)
	}
}

func TestDrainDockerStreamReportsTheDaemonsError(t *testing.T) {
	if err := drainDockerStream(strings.NewReader(`{"stream":"step 1"}` + "\n")); err != nil {
		t.Errorf("drain = %v, want nil", err)
	}
	err := drainDockerStream(strings.NewReader(`{"stream":"step 1"}` + "\n" + `{"error":"no such file"}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("drain = %v, want the daemon's error", err)
	}
}

func TestEnvValue(t *testing.T) {
	env := []string{"PATH=/bin", "EIKAD_TOKEN=secret", "EMPTY="}
	for name, want := range map[string]string{
		"EIKAD_TOKEN": "secret",
		"EMPTY":       "",
		"ABSENT":      "",
	} {
		if got := envValue(env, name); got != want {
			t.Errorf("envValue(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNumericUser(t *testing.T) {
	cases := []struct {
		user     string
		uid, gid int
		ok       bool
	}{
		{"1000:1000", 1000, 1000, true},
		{"1000", 1000, 1000, true},
		{"2000:3000", 2000, 3000, true},
		{"worker", 0, 0, false},
		{"worker:staff", 0, 0, false},
		{"1000:staff", 0, 0, false},
		{"-1:5", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.user, func(t *testing.T) {
			uid, gid, ok := numericUser(c.user)
			if ok != c.ok || (ok && (uid != c.uid || gid != c.gid)) {
				t.Errorf("numericUser(%q) = %d, %d, %v; want %d, %d, %v", c.user, uid, gid, ok, c.uid, c.gid, c.ok)
			}
		})
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
	if strings.Contains(dockerfile, "/workspace") {
		t.Error("the sandbox Dockerfile still mentions /workspace")
	}
}
