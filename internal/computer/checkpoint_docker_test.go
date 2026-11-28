//go:build docker

package computer_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor/sandbox"
)

func TestACheckpointIsKeptOutsideTheComputerRoot(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)

	_, ex := startComputer(t, host, computer.Spec{})
	run(t, ex, "mkdir -p projects/app && echo one > projects/app/a.txt")
	client, ok := ex.(*sandbox.Client)
	if !ok {
		t.Fatalf("executor is %T, want the sandbox client", ex)
	}
	first, err := client.Checkpoint(t.Context(), eikad.CheckpointRequest{Dir: "projects/app", Run: "r1", Label: eikad.CheckpointStart})
	if err != nil {
		t.Fatalf("checkpoint in the real image: %v", err)
	}
	run(t, ex, "echo two > projects/app/b.txt")
	second, err := client.Checkpoint(t.Context(), eikad.CheckpointRequest{Dir: "projects/app", Run: "r1", Label: eikad.CheckpointEnd})
	if err != nil || second.Commit == first.Commit || second.Files != 1 {
		t.Fatalf("second checkpoint = %+v, %v; want a new commit of one file", second, err)
	}

	// The repository is in the container, not under the computer's volume,
	// and the home directory holds no trace of it.
	if got := run(t, ex, "ls -d "+eikad.DefaultCheckpointDir+"/*.git | wc -l"); got != "1" {
		t.Errorf("shadow repositories in %s = %s, want 1", eikad.DefaultCheckpointDir, got)
	}
	if got := run(t, ex, "find "+computer.Root+" -name '*.git' -o -name .git | wc -l"); got != "0" {
		t.Errorf("the computer root holds %s git directories, want none", got)
	}
	if got := strings.TrimSpace(run(t, ex, "git --git-dir "+eikad.DefaultCheckpointDir+"/*.git rev-list --count HEAD")); got != "2" {
		t.Errorf("commits = %s, want 2", got)
	}
}
