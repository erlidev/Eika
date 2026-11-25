package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// modelCallers are the files that send a request to a model provider, each
// with how it takes a lease on the inference queue first. Every model call in
// Core goes through internal/inference; this list is how a new call site
// cannot slip past it unseen. A file that calls Provider.Stream or
// provider.Complete and is not here fails the test: add the lease, then the
// file.
var modelCallers = map[string]string{
	"internal/provider/complete.go": "Complete is the helper its callers hold a lease around",
	"internal/agent/response.go":    "stream takes the run's lease for each attempt",
	"internal/provider/probe/check.go": "the conformance and slot affinity checks take a lease for each of their requests " +
		"(Queue.Do), at maintenance or interactive priority",
	"internal/utility/title.go": "Title runs inside the lease the server takes for the title task",
	"internal/server/models.go": "the model test runs inside a lease at interactive priority",
	"evals/live/scenario.go": "the live suite drives the provider directly to measure it; " +
		"its queue scenarios take their leases from internal/inference themselves",
}

// TestEveryModelCallSiteIsKnown scans the non-test Go files for calls that
// reach a model: a Stream method call, and provider.Complete.
func TestEveryModelCallSiteIsKnown(t *testing.T) {
	fset := token.NewFileSet()
	var found []string
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
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
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		calls := false
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); sel.Sel.Name == "Stream" || (ok && pkg.Name == "provider" && sel.Sel.Name == "Complete") {
				calls = true
			}
			return true
		})
		if calls {
			rel, err := filepath.Rel("..", path)
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range found {
		if _, ok := modelCallers[file]; !ok {
			t.Errorf("%s sends a request to a model provider, which must hold an inference lease (internal/inference); add the lease and list the file in modelCallers", file)
		}
	}
	for file := range modelCallers {
		if !slices.Contains(found, file) {
			t.Errorf("modelCallers lists %s, which no longer calls a model provider", file)
		}
	}
}
