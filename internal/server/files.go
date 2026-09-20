package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/executor"
)

// maxFileBytes bounds a file the editor opens or saves. Anything larger is
// not something a person edits in a browser.
const maxFileBytes = 2 << 20

// binarySniffBytes is how much of a file is searched for a NUL byte, the sign
// of content that is not text.
const binarySniffBytes = 8 << 10

// fileEntry is one file or directory on the wire. The fields match
// eikad.FileInfo, so a listing reads the same from either.
type fileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

// filesResponse is the body of GET /api/workspaces/{id}/files.
type filesResponse struct {
	Entries []fileEntry `json:"entries"`
}

// fileResponse is the body of GET /api/workspaces/{id}/file. Content is empty
// when the file is binary or too large to open, and the flags say which.
type fileResponse struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Binary   bool   `json:"binary"`
	TooLarge bool   `json:"too_large"`
	Content  string `json:"content"`
}

// handleListFiles lists one directory of a workspace, directories first and
// each group by name.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	ex, path, err := s.workspaceFile(r, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	info, err := ex.Stat(r.Context(), path)
	if err != nil {
		s.fail(w, r, fileError(err, path))
		return
	}
	if !info.IsDir {
		s.fail(w, r, invalidf("%s is not a directory", path))
		return
	}
	children, err := ex.List(r.Context(), path)
	if err != nil {
		s.fail(w, r, fileError(err, path))
		return
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].IsDir != children[j].IsDir {
			return children[i].IsDir
		}
		return children[i].Name < children[j].Name
	})
	out := make([]fileEntry, 0, len(children))
	for _, c := range children {
		out = append(out, asFileEntry(c))
	}
	writeJSON(w, s.log, http.StatusOK, filesResponse{Entries: out})
}

// handleReadFile returns one file's text for the editor. A binary file or one
// over maxFileBytes comes back flagged and without content.
func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	ex, path, err := s.workspaceFile(r, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	info, err := ex.Stat(r.Context(), path)
	if err != nil {
		s.fail(w, r, fileError(err, path))
		return
	}
	if info.IsDir {
		s.fail(w, r, invalidf("%s is a directory", path))
		return
	}
	body := fileResponse{Path: info.Path, Size: info.Size}
	if info.Size > maxFileBytes {
		body.TooLarge = true
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}
	// One byte past the bound tells a file that grew since the stat from one
	// that fits.
	data, err := ex.ReadFile(r.Context(), path, executor.ReadOpts{MaxBytes: maxFileBytes + 1})
	if err != nil {
		s.fail(w, r, fileError(err, path))
		return
	}
	switch {
	case len(data) > maxFileBytes:
		body.TooLarge = true
	case bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0:
		body.Binary = true
	default:
		body.Size = int64(len(data))
		body.Content = string(data)
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleWriteFile saves the request body as one file, creating its parent
// directories, and tells the workspace's watchers that its files changed.
func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	ex, path, err := s.workspaceFile(r, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFileBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.fail(w, r, tooLargef("a file saved through the API is at most %d bytes", maxFileBytes))
			return
		}
		s.fail(w, r, invalidf("read request body: %v", err))
		return
	}
	if info, err := ex.Stat(r.Context(), path); err == nil && info.IsDir {
		s.fail(w, r, invalidf("%s is a directory", path))
		return
	}
	if err := ex.WriteFile(r.Context(), path, data); err != nil {
		s.fail(w, r, fileError(err, path))
		return
	}
	info, err := ex.Stat(r.Context(), path)
	if err != nil {
		s.fail(w, r, fileError(err, path))
		return
	}
	s.workspaceChanged(r.Context(), r.PathValue("id"))
	writeJSON(w, s.log, http.StatusOK, asFileEntry(info))
}

// workspaceFile resolves the workspace and the path a file request names.
// The path is checked against the workspace root before anything reaches the
// sandbox, which checks it again after following symlinks.
func (s *Server) workspaceFile(r *http.Request, required bool) (executor.Executor, string, error) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		if required {
			return nil, "", invalidf("path is required")
		}
		path = "."
	}
	if _, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id")); err != nil {
		return nil, "", err
	}
	ex, err := s.executorFor(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, "", err
	}
	if _, err := executor.Resolve(ex.Root(), path); err != nil {
		return nil, "", fileError(err, path)
	}
	return ex, path, nil
}

// fileError maps what an executor reports about a path onto the API's
// statuses: a missing file is 404 and a path the workspace refuses, outside
// its root or unreadable, is 403. Anything else is the harness's failure.
func fileError(err error, path string) error {
	switch {
	case errors.Is(err, executor.ErrPathOutsideRoot), errors.Is(err, fs.ErrPermission):
		return forbiddenf("%s is outside the workspace or not accessible", path)
	case errors.Is(err, executor.ErrNotFound):
		return notFoundf("%s not found", path)
	default:
		return err
	}
}

// workspaceChanged tells the clients watching a workspace that its files or
// its git state changed, so that file and change views refresh. It reuses
// workspace.state with the workspace's unchanged state rather than adding an
// event type: the payload is the same, and a client refreshes on either.
func (s *Server) workspaceChanged(ctx context.Context, id string) {
	ws, err := s.deps.Store.Workspace(ctx, id)
	if err != nil {
		s.log.Warn("read workspace for a change event", "workspace_id", id, "error", err)
		return
	}
	s.workspaceState(ctx, ws.ID, ws.ProjectID, ws.State)
}

// asFileEntry renders an executor's file description on the wire.
func asFileEntry(f executor.FileInfo) fileEntry {
	return fileEntry{
		Name:    f.Name,
		Path:    f.Path,
		Size:    f.Size,
		Mode:    uint32(f.Mode),
		ModTime: f.ModTime.UTC(),
		IsDir:   f.IsDir,
	}
}
