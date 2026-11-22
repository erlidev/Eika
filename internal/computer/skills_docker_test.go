//go:build docker

package computer_test

import (
	"errors"
	"io/fs"
	"os/exec"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/skill/volume"
)

func TestSkillsVolumeOperations(t *testing.T) {
	requireDocker(t)
	requireImage(t)
	host := newHost(t)
	cx, ex := startComputer(t, host, computer.Spec{Root: volume.Root, Role: "skills"})
	if got, _ := exec.Command("docker", "inspect", "-f", "{{index .Config.Labels \"eika.role\"}} {{len .Mounts}}", computer.ContainerName(cx.ID)).Output(); strings.TrimSpace(string(got)) != "skills 1" {
		t.Errorf("role and mounts = %q", got)
	}
	v := volume.New(ex)
	ctx := t.Context()
	files := []skill.File{
		{Path: "SKILL.md", Mode: 0o644, Data: []byte("---\nname: demo\n---\nbody")},
		{Path: "scripts/run.sh", Mode: 0o755, Data: []byte("#!/bin/sh\n")},
	}
	if _, err := v.Hash(ctx, "demo"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Hash before install = %v, want not exist", err)
	}
	if err := v.Stage(ctx, "demo", files); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Hash(ctx, "demo"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("staging installed the skill: %v", err)
	}
	staged, err := v.HashStaged(ctx, "demo")
	if err != nil || len(staged) != 64 {
		t.Fatalf("HashStaged = %q, %v", staged, err)
	}
	if err := v.Replace(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Hash(ctx, "demo"); err != nil || got != staged {
		t.Fatalf("Hash = %q, %v, want %q", got, err, staged)
	}
	if got := run(t, ex, "stat -c %a demo/scripts/run.sh demo/SKILL.md"); strings.Fields(got)[0] != "755" || strings.Fields(got)[1] != "644" {
		t.Errorf("modes = %q", got)
	}

	// Replacing swaps the whole directory: a file the new tree lacks is gone.
	if err := v.Stage(ctx, "demo", files[:1]); err != nil {
		t.Fatal(err)
	}
	if err := v.Replace(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if got := run(t, ex, "ls demo; ls -A . | tr '\\n' ' '"); strings.Contains(got, "scripts") || strings.Contains(got, ".stage/demo") {
		t.Errorf("after replace: %q", got)
	}
	if got, _ := v.Hash(ctx, "demo"); got == staged {
		t.Error("hash did not change with the content")
	}
	// Replace with nothing staged changes nothing.
	if err := v.Replace(ctx, "demo"); err == nil {
		t.Error("Replace with nothing staged succeeded")
	}
	if _, err := v.Hash(ctx, "demo"); err != nil {
		t.Errorf("failed replace lost the skill: %v", err)
	}

	if err := v.Remove(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := v.Remove(ctx, "demo"); err != nil {
		t.Errorf("Remove again: %v", err)
	}
	if _, err := v.Hash(ctx, "demo"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Hash after remove = %v", err)
	}
}
