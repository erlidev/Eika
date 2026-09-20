//go:build docker

package server_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/server"
)

// fileEntryWire is one file or directory as the API reports it.
type fileEntryWire struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

// filesWire is the body of a directory listing.
type filesWire struct {
	Entries []fileEntryWire `json:"entries"`
}

// fileWire is the body of a file read.
type fileWire struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Binary   bool   `json:"binary"`
	TooLarge bool   `json:"too_large"`
	Content  string `json:"content"`
}

// rawRequest sends one API request whose body is raw bytes rather than JSON,
// which is how the editor saves a file.
func rawRequest(t *testing.T, s *server.Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// nextWorkspaceEvent waits for the next event on a subscription.
func nextWorkspaceEvent(t *testing.T, sub *event.Subscription) event.WorkspaceState {
	t.Helper()
	select {
	case e := <-sub.Events():
		if e.Type != event.TypeWorkspaceState {
			t.Fatalf("event type = %q, want workspace.state", e.Type)
		}
		var payload event.WorkspaceState
		if err := e.DecodePayload(&payload); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("no workspace.state event")
		return event.WorkspaceState{}
	}
}

func TestFileRoutes(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	base := "/api/workspaces/" + ws.ID

	if err := os.MkdirAll(filepath.Join(dir, "src", "deep"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "Assets"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write(t, dir, "src/main.go", "package main\n")
	write(t, dir, "b.txt", "bee\n")
	write(t, dir, "A.md", "# a\n")
	write(t, dir, "blob.bin", "text\x00more")
	write(t, dir, "huge.txt", strings.Repeat("x", 2<<20+1))
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "out")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	t.Run("lists the root, directories first", func(t *testing.T) {
		list := decodeBody[filesWire](t, request(t, a.Server, "GET", base+"/files", nil), 200)
		var names []string
		for _, e := range list.Entries {
			names = append(names, e.Name)
		}
		want := ".git,Assets,src,A.md,b.txt,blob.bin,hello.txt,huge.txt,out"
		if got := strings.Join(names, ","); got != want {
			t.Errorf("entries = %s, want %s", got, want)
		}
		for _, e := range list.Entries {
			if e.Name == "b.txt" && (e.Path != "b.txt" || e.Size != 4 || e.IsDir || e.ModTime.IsZero() || e.Mode == 0) {
				t.Errorf("b.txt = %+v", e)
			}
		}
	})

	t.Run("lists a subdirectory with workspace paths", func(t *testing.T) {
		list := decodeBody[filesWire](t, request(t, a.Server, "GET", base+"/files?path=src", nil), 200)
		if len(list.Entries) != 2 || list.Entries[0].Path != "src/deep" || !list.Entries[0].IsDir ||
			list.Entries[1].Path != "src/main.go" {
			t.Errorf("entries = %+v, want src/deep then src/main.go", list.Entries)
		}
	})

	t.Run("reads text", func(t *testing.T) {
		f := decodeBody[fileWire](t, request(t, a.Server, "GET", base+"/file?path=src/main.go", nil), 200)
		if f.Content != "package main\n" || f.Path != "src/main.go" || f.Size != 13 || f.Binary || f.TooLarge {
			t.Errorf("file = %+v", f)
		}
	})

	t.Run("flags binary and oversized files without content", func(t *testing.T) {
		bin := decodeBody[fileWire](t, request(t, a.Server, "GET", base+"/file?path=blob.bin", nil), 200)
		if !bin.Binary || bin.TooLarge || bin.Content != "" {
			t.Errorf("binary file = %+v", bin)
		}
		huge := decodeBody[fileWire](t, request(t, a.Server, "GET", base+"/file?path=huge.txt", nil), 200)
		if !huge.TooLarge || huge.Content != "" || huge.Size != 2<<20+1 {
			t.Errorf("huge file = %+v", huge)
		}
	})

	t.Run("saves a file and reports it", func(t *testing.T) {
		sub := a.Server.Bus().Subscribe(event.WorkspaceTopic(ws.ID))
		defer sub.Close()
		rec := rawRequest(t, a.Server, "PUT", base+"/file?path=new/dir/notes.txt", []byte("saved\n"))
		entry := decodeBody[fileEntryWire](t, rec, 200)
		if entry.Path != "new/dir/notes.txt" || entry.Name != "notes.txt" || entry.Size != 6 || entry.IsDir {
			t.Errorf("entry = %+v", entry)
		}
		data, err := os.ReadFile(filepath.Join(dir, "new", "dir", "notes.txt"))
		if err != nil || string(data) != "saved\n" {
			t.Errorf("file on disk = %q, %v", data, err)
		}
		// Views of the workspace's files and changes refresh on this.
		if got := nextWorkspaceEvent(t, sub); got.WorkspaceID != ws.ID || got.State != "running" {
			t.Errorf("event = %+v, want the running workspace", got)
		}
	})

	t.Run("refuses what it cannot serve", func(t *testing.T) {
		cases := []struct {
			name, method, path string
			body               []byte
			status             int
		}{
			{"a missing directory", "GET", "/files?path=absent", nil, 404},
			{"a file as a directory", "GET", "/files?path=b.txt", nil, 400},
			{"a directory outside the root", "GET", "/files?path=..", nil, 403},
			{"a missing file", "GET", "/file?path=absent.txt", nil, 404},
			{"a directory as a file", "GET", "/file?path=src", nil, 400},
			{"a file outside the root", "GET", "/file?path=../etc/passwd", nil, 403},
			{"a file behind a symlink out of the root", "GET", "/file?path=out/x", nil, 403},
			{"a read without a path", "GET", "/file", nil, 400},
			{"a save outside the root", "PUT", "/file?path=../escape.txt", []byte("x"), 403},
			{"a save over a directory", "PUT", "/file?path=src", []byte("x"), 400},
			{"a save over the size bound", "PUT", "/file?path=big.txt", bytes.Repeat([]byte("x"), 2<<20+1), 413},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				rec := rawRequest(t, a.Server, c.method, base+c.path, c.body)
				if rec.Code != c.status {
					t.Errorf("status = %d, want %d: %s", rec.Code, c.status, rec.Body.String())
				}
			})
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
			t.Error("a save left the workspace")
		}
		if _, err := os.Stat(filepath.Join(dir, "big.txt")); err == nil {
			t.Error("an oversized save was written")
		}
	})

	t.Run("needs a running workspace", func(t *testing.T) {
		if rec := request(t, a.Server, "POST", base+"/stop", nil); rec.Code != 200 {
			t.Fatalf("stop = %d", rec.Code)
		}
		defer request(t, a.Server, "POST", base+"/start", nil)
		if rec := request(t, a.Server, "GET", base+"/files", nil); rec.Code != http.StatusConflict {
			t.Errorf("list in a stopped workspace = %d, want 409", rec.Code)
		}
	})

	if rec := request(t, a.Server, "GET", "/api/workspaces/absent/files", nil); rec.Code != http.StatusNotFound {
		t.Errorf("list in a missing workspace = %d, want 404", rec.Code)
	}
}
