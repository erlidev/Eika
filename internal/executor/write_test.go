package executor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/executor"
)

func TestAtomicWriteFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script")
	if err := os.WriteFile(path, []byte("original"), 0o751); err != nil {
		t.Fatal(err)
	}
	// Replacing a file must not change another hard link to the old inode.
	if err := os.Link(path, filepath.Join(root, "old")); err != nil {
		t.Fatal(err)
	}
	if err := executor.AtomicWriteFile(t.Context(), root, "script", []byte("complete")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o751 {
		t.Fatalf("permissions = %v, %v", info, err)
	}
	for name, want := range map[string]string{"script": "complete", "old": "original"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := executor.AtomicWriteFile(ctx, root, "script", []byte("canceled")); err == nil {
		t.Fatal("accepted canceled write")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "complete" {
		t.Fatalf("canceled write changed file: %q, %v", got, err)
	}
	if err := executor.AtomicWriteFile(t.Context(), root, ".", []byte("directory")); err == nil {
		t.Fatal("replaced directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary file left behind: %v, %v", entries, err)
	}
}

func TestAtomicWriteRejectsSymlinkOutsideRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := executor.AtomicWriteFile(t.Context(), root, "escape/file", []byte("outside")); err == nil {
		t.Fatal("wrote outside root")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside root changed: %v, %v", entries, err)
	}
}
