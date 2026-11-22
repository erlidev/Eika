//go:build docker

package computer_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/computer"
)

func TestSkillsVolumeIsReadOnlyAndAloneInAComputer(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	installer, iex := startComputer(t, host, computer.Spec{Root: "/skills-rw", Role: "skills"})
	run(t, iex, "echo hello > /skills-rw/demo.txt")
	reader, ex := startComputer(t, host, computer.Spec{
		ReadOnly: []computer.ReadOnlyMount{{Volume: installer.Volume, Target: "/skills"}},
	})

	if got := run(t, ex, "cat /skills/demo.txt"); got != "hello" {
		t.Errorf("read /skills/demo.txt = %q", got)
	}
	// Every way to change it must fail: a write, a delete, a remount.
	for _, script := range []string{
		"echo x > /skills/new.txt",
		"rm /skills/demo.txt",
		"mount -o remount,rw /skills",
		"mount -t tmpfs none /skills",
	} {
		if out, err := exec.Command("docker", "exec", computer.ContainerName(reader.ID), "sh", "-c", script).CombinedOutput(); err == nil {
			t.Errorf("%q succeeded in the reader: %s", script, out)
		}
	}
	if got := run(t, iex, "cat /skills-rw/demo.txt; ls /skills-rw"); got != "hello\ndemo.txt" {
		t.Errorf("the installer's volume changed: %q", got)
	}
	// The reader sees nothing of Core's volume, the memory volume or the
	// installer: only its own home and /skills are mounted.
	out, err := exec.Command("docker", "inspect", "-f", "{{range .Mounts}}{{.Destination}}={{.Name}}={{.RW}};{{end}}", computer.ContainerName(reader.ID)).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/eika=" + reader.Volume + "=true;/skills=" + installer.Volume + "=false;"
	if got := strings.TrimSpace(string(out)); got != want && got != "/skills="+installer.Volume+"=false;/home/eika="+reader.Volume+"=true;" {
		t.Errorf("mounts = %q, want %q", got, want)
	}
	run(t, ex, "test ! -e /var/lib/eika && test ! -e /skills-rw")
	if got := run(t, ex, "ls /skills"); got != "demo.txt" {
		t.Errorf("ls /skills = %q", got)
	}
}

func TestRecreateAddsTheMountAndKeepsTheDataVolume(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	installer, _ := startComputer(t, host, computer.Spec{Root: "/skills-rw", Role: "skills"})
	// A computer made before /skills existed, with work in it.
	old, ex := startComputer(t, host, computer.Spec{})
	run(t, ex, "mkdir -p projects/a && echo mine > projects/a/work.txt")
	if got, _ := host.Inspect(t.Context(), old.ID); got.HasMount("/skills") {
		t.Fatal("the old computer already has /skills")
	}
	// A running computer is never recreated.
	if _, err := host.Recreate(t.Context(), old, computer.Spec{}); err == nil {
		t.Fatal("Recreate of a running computer succeeded")
	}
	if err := host.Stop(t.Context(), &old); err != nil {
		t.Fatal(err)
	}

	fresh, err := host.Recreate(t.Context(), old, computer.Spec{
		Image:    old.Image,
		ReadOnly: []computer.ReadOnlyMount{{Volume: installer.Volume, Target: "/skills"}},
	})
	if err != nil {
		t.Fatalf("Recreate: %v", err)
	}
	if fresh.ContainerID == old.ContainerID || fresh.Token == old.Token {
		t.Error("Recreate kept the old container or token")
	}
	if !volumeExists(old.Volume) {
		t.Fatalf("data volume %s is gone", old.Volume)
	}
	if err := host.Start(t.Context(), &fresh); err != nil {
		t.Fatal(err)
	}
	ex, err = host.Executor(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, ex, "cat projects/a/work.txt"); got != "mine" {
		t.Errorf("work file after recreate = %q, want mine", got)
	}
	if got := run(t, ex, "stat -c %u:%g projects/a/work.txt; ls -d /skills"); got != "1000:1000\n/skills" {
		t.Errorf("owner and /skills = %q", got)
	}
	again, err := host.Inspect(t.Context(), old.ID)
	if err != nil || !again.HasMount("/skills") || again.Volume != old.Volume {
		t.Errorf("inspect after = %+v, %v", again, err)
	}
}
