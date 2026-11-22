//go:build docker

package computer_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/memory/repo"
)

func TestMemoryRepositoryThroughSandbox(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)
	cx, ex := startComputer(t, host, computer.Spec{Root: "/var/lib/eika/memory", Role: "memory"})
	if got := run(t, ex, "pwd"); got != "/var/lib/eika/memory" {
		t.Errorf("root = %q", got)
	}
	if got, _ := exec.Command("docker", "inspect", "-f", "{{index .Config.Labels \"eika.role\"}} {{len .Mounts}}", computer.ContainerName(cx.ID)).Output(); strings.TrimSpace(string(got)) != "memory 1" {
		t.Errorf("role and mounts = %q", got)
	}
	r := repo.NewRepository(ex)
	ctx := t.Context()
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Write(ctx, "metric", []byte("metric units")); err != nil {
		t.Fatal(err)
	}
	if err := r.Commit(ctx, "metric"); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(ctx, "metric"); err != nil || string(got) != "metric units" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if got := run(t, ex, "git log -1 --format='%an <%ae>'"); strings.TrimSpace(got) != "Eika <eika@localhost>" {
		t.Errorf("author = %q", got)
	}
}
