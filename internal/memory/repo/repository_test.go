package repo_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/memory/repo"
)

func TestRepository(t *testing.T) {
	ctx := context.Background()
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := repo.NewRepository(ex)
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape", "-option", "a/b", ".git", ""} {
		if err := r.Write(ctx, name, []byte("bad")); err == nil {
			t.Errorf("write %q succeeded", name)
		}
		if _, err := r.Read(ctx, name); err == nil {
			t.Errorf("read %q succeeded", name)
		}
		if err := r.Commit(ctx, name, "m"); err == nil {
			t.Errorf("commit %q succeeded", name)
		}
	}
	if err := r.Write(ctx, "metric", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if data, err := r.Read(ctx, "metric"); err != nil || string(data) != "hello" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if status, err := r.Status(ctx); err != nil || !strings.Contains(status, "metric.md") {
		t.Fatalf("status = %q, %v", status, err)
	}
	if err := r.Commit(ctx, "metric", "m"); err != nil {
		t.Fatal(err)
	}
	if status, err := r.Status(ctx); err != nil || status != "" {
		t.Fatalf("status after commit = %q, %v", status, err)
	}
	// Remove deletes the file in a commit and keeps it in history.
	if err := r.Remove(ctx, "metric", "Purge memory: metric"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(ctx, "metric"); err == nil {
		t.Error("file readable after remove")
	}
	if status, err := r.Status(ctx); err != nil || status != "" {
		t.Fatalf("status after remove = %q, %v", status, err)
	}
	if err := r.Remove(ctx, "metric", "again"); err != nil {
		t.Fatalf("removing a missing file: %v", err)
	}
	var out bytes.Buffer
	if _, err := ex.Exec(ctx, executor.ExecSpec{Command: "git", Args: []string{"show", "HEAD~1:metric.md"}, Stdout: &out}); err != nil || out.String() != "hello" {
		t.Fatalf("history lost the removed file: %q, %v", out.String(), err)
	}
}
