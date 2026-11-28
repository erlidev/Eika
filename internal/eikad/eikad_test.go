package eikad_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/erlidev/eika/internal/eikad"
)

const testToken = "test-token"

// testLogger returns a logger that discards everything.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newDaemon starts a daemon over a temporary computer root and returns its
// base URL and that root.
func newDaemon(t *testing.T, opts eikad.Options) (string, string) {
	t.Helper()
	if opts.Root == "" {
		opts.Root = t.TempDir()
	}
	if opts.Token == "" {
		opts.Token = testToken
	}
	d, err := eikad.New(opts, testLogger())
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	return srv.URL, d.Root()
}

// do sends an authenticated request to the daemon.
func do(t *testing.T, method, url string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestNewRejectsAnUnusableDaemon(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cases := map[string]eikad.Options{
		"no token":       {Root: dir},
		"missing root":   {Root: filepath.Join(dir, "absent"), Token: testToken},
		"root is a file": {Root: file, Token: testToken},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := eikad.New(opts, testLogger()); err == nil {
				t.Error("New accepted unusable options")
			}
		})
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestEveryOtherRouteNeedsTheToken(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	cases := []struct{ method, path string }{
		{http.MethodPost, "/exec"},
		{http.MethodGet, "/files?path=x"},
		{http.MethodPut, "/files?path=x"},
		{http.MethodGet, "/stat?path=."},
		{http.MethodGet, "/list?path=."},
		{http.MethodPut, "/environment"},
		{http.MethodPost, "/checkpoint"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			for name, token := range map[string]string{"none": "", "wrong": "Bearer nope"} {
				req, err := http.NewRequestWithContext(t.Context(), c.method, base+c.path, nil)
				if err != nil {
					t.Fatalf("build request: %v", err)
				}
				if token != "" {
					req.Header.Set("Authorization", token)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("request: %v", err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("%s token: status = %d, want 401", name, resp.StatusCode)
				}
			}
		})
	}
}

func TestFileRoundTrip(t *testing.T) {
	base, root := newDaemon(t, eikad.Options{})

	resp := do(t, http.MethodPut, base+"/files?path=deep/nested/hello.txt", strings.NewReader("hello"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write status = %d, want 200", resp.StatusCode)
	}
	var written eikad.FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&written); err != nil {
		t.Fatalf("decode write response: %v", err)
	}
	if written.Path != filepath.Join("deep", "nested", "hello.txt") || written.Size != 5 {
		t.Errorf("written = %+v, want the nested file with 5 bytes", written)
	}
	if _, err := os.Stat(filepath.Join(root, "deep", "nested", "hello.txt")); err != nil {
		t.Fatalf("file was not created: %v", err)
	}

	resp = do(t, http.MethodGet, base+"/files?path=deep/nested/hello.txt", nil)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("read = %q, want hello", body)
	}

	resp = do(t, http.MethodGet, base+"/files?path=deep/nested/hello.txt&max_bytes=2", nil)
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "he" {
		t.Errorf("truncated read = %q, want he", body)
	}

	resp = do(t, http.MethodGet, base+"/list?path=deep/nested", nil)
	var list eikad.ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Entries) != 1 || list.Entries[0].Name != "hello.txt" {
		t.Errorf("list = %+v, want one entry named hello.txt", list.Entries)
	}
}

func TestWritingAReadOnlyFileIsForbidden(t *testing.T) {
	base, root := newDaemon(t, eikad.Options{})
	if err := os.WriteFile(filepath.Join(root, "locked"), []byte("original"), 0o444); err != nil {
		t.Fatal(err)
	}
	if resp := do(t, http.MethodPut, base+"/files?path=locked", strings.NewReader("changed")); resp.StatusCode != http.StatusForbidden {
		t.Errorf("read-only write status = %d, want 403", resp.StatusCode)
	}
	if got, err := os.ReadFile(filepath.Join(root, "locked")); err != nil || string(got) != "original" {
		t.Errorf("read-only file = %q, %v", got, err)
	}
}

func TestMissingFileIsNotFound(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	resp := do(t, http.MethodGet, base+"/files?path=absent", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPathsAreConfinedToTheRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "computer")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("make root: %v", err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// A symlink inside the root that points out of it must not be a way out.
	if err := os.Symlink(dir, filepath.Join(root, "out")); err != nil {
		t.Fatalf("make symlink: %v", err)
	}
	base, _ := newDaemon(t, eikad.Options{Root: root})

	escapes := []string{
		"deep/../../secret.txt",
		"out/secret.txt",
		url.QueryEscape(secret),
	}
	for _, path := range escapes {
		t.Run(path, func(t *testing.T) {
			if resp := do(t, http.MethodGet, base+"/files?path="+path, nil); resp.StatusCode != http.StatusForbidden {
				t.Errorf("read status = %d, want 403", resp.StatusCode)
			}
			if resp := do(t, http.MethodPut, base+"/files?path="+path, strings.NewReader("x")); resp.StatusCode != http.StatusForbidden {
				t.Errorf("write status = %d, want 403", resp.StatusCode)
			}
			if resp := do(t, http.MethodGet, base+"/stat?path="+path, nil); resp.StatusCode != http.StatusForbidden {
				t.Errorf("stat status = %d, want 403", resp.StatusCode)
			}
		})
	}
	if data, err := os.ReadFile(secret); err != nil || string(data) != "secret" {
		t.Errorf("the secret was overwritten: %q, %v", data, err)
	}

	// A file created through a symlink inside the root stays inside it: the
	// daemon opens the canonical path it checked, not the one it was given.
	if err := os.Symlink(".", filepath.Join(root, "self")); err != nil {
		t.Fatalf("make symlink: %v", err)
	}
	resp := do(t, http.MethodPut, base+"/files?path=self/through-a-link.txt", strings.NewReader("x"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("write through a link inside the root: status %d", resp.StatusCode)
	}
	if _, err := os.Lstat(filepath.Join(root, "through-a-link.txt")); err != nil {
		t.Errorf("the file did not land in the root: %v", err)
	}
}

// A root at /home/eika has siblings, unlike the old /workspace: other homes
// and a directory that only shares the root's name as a prefix. None of them,
// by any spelling, is inside the root.
func TestPathsAreConfinedToARootBesideItsSiblings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	root := filepath.Join(home, "eika")
	for _, dir := range []string{root, filepath.Join(home, "eikaother"), filepath.Join(home, "other")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("private"), 0o600); err != nil {
			t.Fatalf("write private file: %v", err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "other"), filepath.Join(root, "link-out")); err != nil {
		t.Fatalf("make symlink: %v", err)
	}
	base, _ := newDaemon(t, eikad.Options{Root: root})

	escapes := []string{
		"../eikaother/private.txt",
		"link-out/private.txt",
		url.QueryEscape(filepath.Join(home, "eikaother", "private.txt")),
		url.QueryEscape(root + "/../other/private.txt"),
	}
	for _, path := range escapes {
		t.Run(path, func(t *testing.T) {
			if resp := do(t, http.MethodGet, base+"/files?path="+path, nil); resp.StatusCode != http.StatusForbidden {
				t.Errorf("read status = %d, want 403", resp.StatusCode)
			}
			if resp := do(t, http.MethodPut, base+"/files?path="+path, strings.NewReader("x")); resp.StatusCode != http.StatusForbidden {
				t.Errorf("write status = %d, want 403", resp.StatusCode)
			}
		})
	}
	for _, dir := range []string{"other", "eikaother"} {
		if data, err := os.ReadFile(filepath.Join(home, dir, "private.txt")); err != nil || string(data) != "private" {
			t.Errorf("%s/private.txt was overwritten: %q, %v", dir, data, err)
		}
	}
}

// execFrames runs a command and returns its output streams and final frame.
func execFrames(t *testing.T, base string, req eikad.ExecRequest) (string, string, eikad.ExecFrame) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	resp := do(t, http.MethodPost, base+"/exec", strings.NewReader(string(body)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exec status = %d, want 200", resp.StatusCode)
	}
	var stdout, stderr strings.Builder
	var last eikad.ExecFrame
	dec := json.NewDecoder(resp.Body)
	for {
		var frame eikad.ExecFrame
		if err := dec.Decode(&frame); err != nil {
			if err == io.EOF {
				return stdout.String(), stderr.String(), last
			}
			t.Fatalf("decode frame: %v", err)
		}
		switch frame.Stream {
		case "stdout":
			stdout.Write(frame.Data)
		case "stderr":
			stderr.Write(frame.Data)
		default:
			last = frame
		}
	}
}

func TestExec(t *testing.T) {
	base, root := newDaemon(t, eikad.Options{})
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("make sub: %v", err)
	}

	t.Run("streams output and reports success", func(t *testing.T) {
		stdout, _, last := execFrames(t, base, eikad.ExecRequest{Command: "echo", Args: []string{"hi"}})
		if stdout != "hi\n" {
			t.Errorf("stdout = %q, want hi", stdout)
		}
		if last.ExitCode == nil || *last.ExitCode != 0 {
			t.Errorf("final frame = %+v, want exit code 0", last)
		}
	})

	t.Run("reports a non-zero exit and stderr", func(t *testing.T) {
		_, stderr, last := execFrames(t, base, eikad.ExecRequest{
			Command: "echo boom >&2; exit 3",
			Shell:   true,
		})
		if !strings.Contains(stderr, "boom") {
			t.Errorf("stderr = %q, want boom", stderr)
		}
		if last.ExitCode == nil || *last.ExitCode != 3 {
			t.Errorf("final frame = %+v, want exit code 3", last)
		}
	})

	t.Run("runs in the root by default", func(t *testing.T) {
		stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: "pwd", Shell: true})
		if strings.TrimSpace(stdout) != root {
			t.Errorf("pwd = %q, want %q", strings.TrimSpace(stdout), root)
		}
	})

	t.Run("runs in the requested directory", func(t *testing.T) {
		stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: "pwd", Shell: true, Dir: "sub"})
		if strings.TrimSpace(stdout) != filepath.Join(root, "sub") {
			t.Errorf("pwd = %q, want the sub directory", strings.TrimSpace(stdout))
		}
	})

	t.Run("feeds stdin", func(t *testing.T) {
		stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: "cat", Stdin: []byte("from stdin")})
		if stdout != "from stdin" {
			t.Errorf("stdout = %q, want from stdin", stdout)
		}
	})

	t.Run("reports a timeout", func(t *testing.T) {
		_, _, last := execFrames(t, base, eikad.ExecRequest{
			Command:   "sleep 10",
			Shell:     true,
			TimeoutMS: 100,
		})
		if !last.TimedOut {
			t.Errorf("final frame = %+v, want timed_out", last)
		}
	})

	t.Run("times out even when a child holds the pipes", func(t *testing.T) {
		done := make(chan eikad.ExecFrame, 1)
		go func() {
			_, _, last := execFrames(t, base, eikad.ExecRequest{
				Command:   "sleep 30 & sleep 30",
				Shell:     true,
				TimeoutMS: 1000,
			})
			done <- last
		}()
		select {
		case last := <-done:
			if !last.TimedOut {
				t.Errorf("final frame = %+v, want timed_out", last)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("the timeout did not end the run: a backgrounded child kept it alive")
		}
	})

	t.Run("truncates runaway output", func(t *testing.T) {
		stdout, _, last := execFrames(t, base, eikad.ExecRequest{
			Command: "yes eika | head -c 20000000",
			Shell:   true,
		})
		if !last.Truncated {
			t.Errorf("final frame = %+v, want truncated", last)
		}
		if len(stdout) > 9<<20 {
			t.Errorf("streamed %d bytes, want the daemon's cap", len(stdout))
		}
	})

	t.Run("hides the daemon token", func(t *testing.T) {
		t.Setenv(eikad.TokenEnv, testToken)
		base, _ := newDaemon(t, eikad.Options{})
		stdout, _, _ := execFrames(t, base, eikad.ExecRequest{Command: "env", Shell: true})
		if strings.Contains(stdout, eikad.TokenEnv) {
			t.Errorf("the command saw %s in its environment", eikad.TokenEnv)
		}
	})

	t.Run("rejects a directory outside the root", func(t *testing.T) {
		body, err := json.Marshal(eikad.ExecRequest{Command: "pwd", Dir: "../.."})
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		resp := do(t, http.MethodPost, base+"/exec", strings.NewReader(string(body)))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.StatusCode)
		}
	})

}

// dial opens an authenticated WebSocket to the daemon.
func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}
