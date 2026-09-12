package eikad

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot reports a path that leaves the workspace root, whether by
// traversal, by being absolute, or through a symlink.
var ErrOutsideRoot = errors.New("path is outside the workspace root")

// resolve maps a request path to an absolute path inside the root. The path
// may be relative to the root or absolute inside it; ".." and symlinks that
// point out of the root are rejected.
func (d *Daemon) resolve(path string) (string, error) {
	rel, err := relativeTo(d.root, path)
	if err != nil {
		return "", err
	}
	full := filepath.Join(d.root, rel)
	real, err := resolveExisting(full)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if !within(d.root, real) {
		return "", ErrOutsideRoot
	}
	return full, nil
}

// relativeTo turns a request path into a clean path relative to root.
func relativeTo(root, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ".", nil
	}
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err != nil {
			return "", ErrOutsideRoot
		}
		path = rel
	}
	// Traversal is rejected rather than clamped, so that a caller never gets
	// a different file than the one it asked for.
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	return cleaned, nil
}

// resolveExisting evaluates the symlinks of the longest existing prefix of p
// and appends the part that does not exist yet, so that a path being created
// is checked against the directory it would be created in.
func resolveExisting(p string) (string, error) {
	cur, rest := p, ""
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether p is root or lies under it.
func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
