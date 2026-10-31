package internal

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// module is the import path every package of the repository starts with.
const module = "github.com/erlidev/eika/"

// boundary is one import rule: no package under From may import a package
// under To. Allowed lists the importing packages that are exempt, each with
// the reason in a comment.
type boundary struct {
	from, to string
	allowed  []string
}

// boundaries are the dependency rules AGENTS.md states. Paths are relative to
// the module root, and a rule covers a package and every package below it.
// Phase 4 adds the Sentry rules here.
var boundaries = []boundary{
	// Nothing imports server; only the binaries wire it.
	{from: "", to: "internal/server", allowed: []string{
		"cmd/eika",
		"cmd/eikad", // server.Serve, the one HTTP listener both binaries share
	}},
	// Tools reach a computer only through the executor.
	{from: "internal/tool", to: "internal/computer"},
	// Tools do not depend on the sandbox daemon.
	{from: "internal/tool", to: "internal/eikad", allowed: []string{
		"internal/tool/builtin", // eikad.BinaryPath, the binary web_fetch's filter runs in the sandbox
	}},
}

// TestImportBoundaries checks every non-test Go file in the repository
// against the import rules, so a dependency that points the wrong way fails
// make check rather than review.
func TestImportBoundaries(t *testing.T) {
	imports := packageImports(t, "..")
	if len(imports) == 0 {
		t.Fatal("no packages found")
	}
	for pkg, deps := range imports {
		for _, dep := range deps {
			for _, b := range boundaries {
				if !under(pkg, b.from) || !under(dep, b.to) || under(pkg, b.to) {
					continue
				}
				if !slices.Contains(b.allowed, pkg) {
					t.Errorf("%s imports %s, which the boundary %q -> %q forbids", pkg, dep, b.from, b.to)
				}
			}
		}
	}
}

// leaves are the packages that import nothing of the module, so that any
// package may import them without a cycle and their tests need no fake of
// another package. Each has the reason it must stay one.
var leaves = map[string]string{
	// The queue every model call waits in. Callers describe pools and calls
	// to it in its own terms, so the agent, the probe, the utility tasks and
	// the server can all hold a lease without it knowing any of them.
	"internal/inference": "the inference queue is imported by every package that makes model calls",
}

// TestLeavesImportNothingOfTheModule checks the packages in leaves.
func TestLeavesImportNothingOfTheModule(t *testing.T) {
	imports := packageImports(t, "..")
	for pkg, reason := range leaves {
		deps, ok := imports[pkg]
		if !ok {
			t.Errorf("leaf %s has no Go package (%s)", pkg, reason)
			continue
		}
		for _, dep := range deps {
			t.Errorf("%s imports %s, but it must import only the standard library: %s", pkg, dep, reason)
		}
	}
}

// TestBoundariesNameRealPackages keeps the rules from going stale when a
// package is renamed or removed.
func TestBoundariesNameRealPackages(t *testing.T) {
	imports := packageImports(t, "..")
	exists := func(path string) bool {
		for pkg := range imports {
			if under(pkg, path) {
				return true
			}
		}
		return false
	}
	for _, b := range boundaries {
		for _, path := range append([]string{b.from, b.to}, b.allowed...) {
			if path != "" && !exists(path) {
				t.Errorf("boundary %q -> %q names %s, which has no Go package", b.from, b.to, path)
			}
		}
	}
}

// packageImports maps each package directory, relative to root, to the
// module packages its non-test files import.
func packageImports(t *testing.T, root string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || (name != ".." && strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		if _, ok := out[pkg]; !ok {
			out[pkg] = nil
		}
		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if dep, ok := strings.CutPrefix(p, module); ok {
				out[pkg] = append(out[pkg], dep)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// under reports whether pkg is dir or a package below it. The empty dir
// covers every package.
func under(pkg, dir string) bool {
	return dir == "" || pkg == dir || strings.HasPrefix(pkg, dir+"/")
}
