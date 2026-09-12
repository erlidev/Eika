package executor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrPathOutsideRoot reports that a path resolves outside the workspace root.
// Every executor rejects such a path, so a tool cannot reach the harness
// filesystem by walking up from the workspace.
var ErrPathOutsideRoot = errors.New("path is outside the workspace root")

// Resolve turns a workspace-relative path into an absolute path under root.
// An absolute path is accepted only when it already lies inside root. The
// check is lexical: an executor that follows symlinks checks the resolved
// target as well.
func Resolve(root, path string) (string, error) {
	if root == "" {
		return "", errors.New("resolve path: root is empty")
	}
	root = filepath.Clean(root)
	p := filepath.FromSlash(path)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("resolve path %q: %w", path, ErrPathOutsideRoot)
	}
	return p, nil
}

// Rel reports the workspace-relative, slash-separated form of an absolute path
// under root. The root itself is ".".
func Rel(root, abs string) (string, error) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(abs))
	if err != nil {
		return "", fmt.Errorf("relativize path %q: %w", abs, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relativize path %q: %w", abs, ErrPathOutsideRoot)
	}
	return filepath.ToSlash(rel), nil
}
