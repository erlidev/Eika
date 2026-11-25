package builtin_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	sandboxexec "github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestFileTools(t *testing.T) {
	for _, backend := range []string{"local", "sandbox"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			var e executor.Executor
			if backend == "local" {
				var err error
				e, err = local.New(root)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				d, err := eikad.New(eikad.Options{Root: root, Token: "test"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
				if err != nil {
					t.Fatal(err)
				}
				e, err = sandboxexec.New(sandboxexec.Options{BaseURL: "http://sandbox", Root: root, Token: "test", HTTPClient: &http.Client{Transport: fileTransport{handler: d.Handler()}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			r, err := builtin.Registry(builtin.Deps{})
			if err != nil {
				t.Fatal(err)
			}
			callRaw := func(t *testing.T, name string, raw []byte) tool.Result {
				t.Helper()
				tl, ok := r.Get(name)
				if !ok {
					t.Fatalf("missing tool %s", name)
				}
				res, err := tl.Call(t.Context(), tool.CallContext{Exec: e}, raw)
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			call := func(t *testing.T, name string, args map[string]string, wantError bool) tool.Result {
				t.Helper()
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				res := callRaw(t, name, raw)
				if res.IsError != wantError {
					t.Fatalf("%s: %+v, want error %v", name, res, wantError)
				}
				return res
			}
			read := func(t *testing.T, path, want string) {
				t.Helper()
				got, err := e.ReadFile(t.Context(), path, executor.ReadOpts{})
				if err != nil || string(got) != want {
					t.Fatalf("read %s = %q, %v; want %q", path, got, err, want)
				}
			}
			t.Run("create read edit delete and empty file", func(t *testing.T) {
				call(t, "file_write", map[string]string{"path": "dir/text", "content": "héllo\nworld\n"}, false)
				if res := call(t, "file_read", map[string]string{"path": "dir/text"}, false); res.Content != "héllo\nworld\n" {
					t.Fatal(res)
				}
				call(t, "file_edit", map[string]string{"path": "dir/text", "old_text": "héllo\nworld", "new_text": "new"}, false)
				read(t, "dir/text", "new\n")
				call(t, "file_edit", map[string]string{"path": "dir/text", "old_text": "new\n", "new_text": ""}, false)
				read(t, "dir/text", "")
				call(t, "file_write", map[string]string{"path": "empty", "content": ""}, false)
				if res := call(t, "file_read", map[string]string{"path": "empty"}, false); res.Content != "(empty file)" {
					t.Fatal(res)
				}
				call(t, "file_write", map[string]string{"path": filepath.Join(root, "empty"), "content": "absolute"}, false)
				read(t, "empty", "absolute")
			})
			t.Run("atomic replacement keeps permissions and symlink targets", func(t *testing.T) {
				call(t, "file_write", map[string]string{"path": "target", "content": "original"}, false)
				target := filepath.Join(root, "target")
				if err := os.Chmod(target, 0o751); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, filepath.Join(root, "hardlink")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, "alias")); err != nil {
					t.Fatal(err)
				}
				call(t, "file_edit", map[string]string{"path": "alias", "old_text": "original", "new_text": "complete"}, false)
				read(t, "target", "complete")
				read(t, "hardlink", "original")
				info, err := os.Stat(target)
				if err != nil || info.Mode().Perm() != 0o751 {
					t.Fatalf("permissions = %v, %v", info, err)
				}
				info, err = os.Lstat(filepath.Join(root, "alias"))
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("alias replaced: %v, %v", info, err)
				}
			})
			t.Run("failed edits leave original", func(t *testing.T) {
				for _, old := range []string{"missing", "abc", "aa", ""} {
					call(t, "file_write", map[string]string{"path": "edit", "content": "abc abc aaa"}, false)
					call(t, "file_edit", map[string]string{"path": "edit", "old_text": old, "new_text": "x"}, true)
					read(t, "edit", "abc abc aaa")
				}
			})
			t.Run("byte limits", func(t *testing.T) {
				text := strings.Repeat("é", 16*1024)
				call(t, "file_write", map[string]string{"path": "limit", "content": text}, false)
				call(t, "file_read", map[string]string{"path": "limit"}, false)
				call(t, "file_write", map[string]string{"path": "limit", "content": text + "x"}, false)
				call(t, "file_read", map[string]string{"path": "limit"}, true)
				large := strings.Repeat("a", 1<<20)
				call(t, "file_write", map[string]string{"path": "limit", "content": large}, false)
				call(t, "file_write", map[string]string{"path": "limit", "content": large + "x"}, true)
				read(t, "limit", large)
				call(t, "file_edit", map[string]string{"path": "limit", "old_text": large, "new_text": large}, false)
				call(t, "file_write", map[string]string{"path": "limit", "content": "x" + large[1:]}, false)
				call(t, "file_edit", map[string]string{"path": "limit", "old_text": "x", "new_text": "xx"}, true)
				read(t, "limit", "x"+large[1:])
				if err := e.WriteFile(t.Context(), "limit", []byte(large+"x")); err != nil {
					t.Fatal(err)
				}
				call(t, "file_edit", map[string]string{"path": "limit", "old_text": "x", "new_text": ""}, true)
			})
			t.Run("invalid arguments", func(t *testing.T) {
				for _, raw := range []string{
					`[]`, `{}`, `{"path":"empty","other":"x"}`, `{"path":""}`, `{"path":"a\u0000b"}`,
					`{"path":"empty"} {}`, `{"path":"empty","path":"edit"}`, `{"path":"\ud800"}`, "{\"path\":\"\xff\"}",
				} {
					if res := callRaw(t, "file_read", []byte(raw)); !res.IsError {
						t.Errorf("accepted %q", raw)
					}
				}
				for _, raw := range []string{`{"path":"empty"}`, `{"path":"empty","content":null}`, `{"path":"empty","content":"\ud800"}`} {
					if res := callRaw(t, "file_write", []byte(raw)); !res.IsError {
						t.Errorf("accepted %q", raw)
					}
				}
				if res := callRaw(t, "file_write", []byte(`{"path":"unicode","content":"\ud83d\ude00\ufffd\\uD800"}`)); res.IsError {
					t.Fatal(res)
				}
				read(t, "unicode", "😀�\\uD800")
			})
			t.Run("binary files", func(t *testing.T) {
				for _, content := range []string{"a\x00b", "a\xffb"} {
					if err := e.WriteFile(t.Context(), "binary", []byte(content)); err != nil {
						t.Fatal(err)
					}
					call(t, "file_read", map[string]string{"path": "binary"}, true)
					call(t, "file_edit", map[string]string{"path": "binary", "old_text": "a", "new_text": "b"}, true)
					read(t, "binary", content)
				}
			})
			t.Run("confinement", func(t *testing.T) {
				outside := t.TempDir()
				secret := filepath.Join(outside, "secret")
				if err := os.WriteFile(secret, []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{"../secret", secret, "escape/secret", "escape/new/file"} {
					call(t, "file_read", map[string]string{"path": path}, true)
					call(t, "file_write", map[string]string{"path": path, "content": "changed"}, true)
					call(t, "file_edit", map[string]string{"path": path, "old_text": "untouched", "new_text": "changed"}, true)
				}
				got, err := os.ReadFile(secret)
				if err != nil || string(got) != "untouched" {
					t.Fatalf("outside file = %q, %v", got, err)
				}
				if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
					t.Fatalf("outside directory created: %v", err)
				}
			})
		})
	}
}

// fileTransport exercises the real daemon without opening a network listener.
type fileTransport struct{ handler http.Handler }

func (f fileTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func TestFileToolsRejectIncompleteOrGrowingRead(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{3, 40000} {
		for _, name := range []string{"file_read", "file_edit"} {
			e := &changingFile{size: size}
			tl, _ := r.Get(name)
			raw := json.RawMessage(`{"path":"text"}`)
			if name == "file_edit" {
				raw = json.RawMessage(`{"path":"text","old_text":"old","new_text":"new"}`)
			}
			res, err := tl.Call(t.Context(), tool.CallContext{Exec: e}, raw)
			if err != nil || !res.IsError || e.wrote {
				t.Fatalf("%s size %d: %+v, %v, wrote %v", name, size, res, err, e.wrote)
			}
		}
	}
}

// changingFile returns a different size at read time and records writes.
type changingFile struct {
	executor.Executor
	size  int64
	wrote bool
}

func (*changingFile) Root() string { return "/computer" }
func (e *changingFile) Stat(context.Context, string) (executor.FileInfo, error) {
	return executor.FileInfo{Size: e.size, Mode: 0o644}, nil
}
func (e *changingFile) ReadFile(_ context.Context, _ string, opts executor.ReadOpts) ([]byte, error) {
	if e.size > 3 {
		return []byte("old"), nil
	}
	return []byte(strings.Repeat("old", int(opts.MaxBytes)/3+1))[:opts.MaxBytes], nil
}
func (e *changingFile) WriteFile(context.Context, string, []byte) error {
	e.wrote = true
	return nil
}
