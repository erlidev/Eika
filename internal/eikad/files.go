package eikad

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// Permissions for files and directories the daemon creates. The sandbox is
// single-user, so the owner's bits are what matter.
const (
	filePerm = 0o644
	dirPerm  = 0o755
)

// handleReadFile streams a file's contents, truncated at ?max_bytes= or at the
// daemon's limit, whichever is smaller.
func (d *Daemon) handleReadFile(w http.ResponseWriter, r *http.Request) {
	path, err := d.requestPath(r)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	max, err := d.readLimit(r.URL.Query().Get("max_bytes"))
	if err != nil {
		writeError(w, d.log, http.StatusBadRequest, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	if fi.IsDir() {
		writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("read %s: is a directory", d.rel(path)))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, io.LimitReader(f, max)); err != nil {
		d.log.Error("stream file", "path", d.rel(path), "error", err)
	}
}

// handleWriteFile writes the request body to a file, creating its parent
// directories, and reports the resulting file.
func (d *Daemon) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	path, err := d.requestPath(r)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWriteBytes))
	if err != nil {
		writeError(w, d.log, http.StatusRequestEntityTooLarge, fmt.Errorf("read request body: %w", err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		writeError(w, d.log, statusFor(err), fmt.Errorf("create parent of %s: %w", d.rel(path), err))
		return
	}
	if err := os.WriteFile(path, data, filePerm); err != nil {
		writeError(w, d.log, statusFor(err), fmt.Errorf("write %s: %w", d.rel(path), err))
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	writeJSON(w, d.log, http.StatusOK, d.fileInfo(path, fi))
}

// handleStat describes one file.
func (d *Daemon) handleStat(w http.ResponseWriter, r *http.Request) {
	path, err := d.requestPath(r)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	writeJSON(w, d.log, http.StatusOK, d.fileInfo(path, fi))
}

// handleList describes the direct children of a directory.
func (d *Daemon) handleList(w http.ResponseWriter, r *http.Request) {
	path, err := d.requestPath(r)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	out := ListResponse{Entries: make([]FileInfo, 0, len(entries))}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			// The entry disappeared between the read and the stat.
			continue
		}
		out.Entries = append(out.Entries, d.fileInfo(filepath.Join(path, e.Name()), fi))
	}
	writeJSON(w, d.log, http.StatusOK, out)
}

// readLimit parses the ?max_bytes= parameter, bounded by the daemon's limit.
func (d *Daemon) readLimit(raw string) (int64, error) {
	if raw == "" {
		return d.maxRead, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse max_bytes: %w", err)
	}
	if n <= 0 {
		return 0, errors.New("parse max_bytes: must be positive")
	}
	return min(n, d.maxRead), nil
}
