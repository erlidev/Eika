package executor

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile replaces a file under root with a complete new file. The
// caller resolves symlinks first; rooted operations also prevent path races
// from writing outside root. Existing permissions are kept.
func AtomicWriteFile(ctx context.Context, root, path string, data []byte) error {
	abs, err := Resolve(root, path)
	if err != nil {
		return err
	}
	rel, err := Rel(root, abs)
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open write root: %w", err)
	}
	defer r.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := r.Stat(rel); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("write %q: not a regular file", path)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat write target: %w", err)
	}
	if err := r.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return fmt.Errorf("create write parent: %w", err)
	}
	tmp := filepath.Join(filepath.Dir(rel), ".eika-write-"+rand.Text())
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = r.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("set file permissions: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.Rename(tmp, rel); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
