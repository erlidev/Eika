package eikad

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Defaults for the bounds the daemon puts on a request.
const (
	// defaultMaxReadBytes caps a file read when the caller asks for no limit.
	defaultMaxReadBytes = 16 << 20
	// maxWriteBytes caps the body of a file write.
	maxWriteBytes = 64 << 20
)

// Options configures a Daemon.
type Options struct {
	// Root is the computer directory every path is confined to.
	Root string
	// Token is the bearer token every route but /healthz requires.
	Token string
	// MaxReadBytes caps a single file read. Zero uses the default.
	MaxReadBytes int64
	// CheckpointDir holds the shadow repositories of project trees. It must
	// lie outside Root. Empty uses DefaultCheckpointDir.
	CheckpointDir string
}

// Daemon serves the sandbox API over HTTP for one computer.
type Daemon struct {
	root    string
	token   string
	log     *slog.Logger
	maxRead int64
	mux     *http.ServeMux

	// ckptDir holds the shadow repositories; ckptMu guards ckptRepos.
	ckptDir   string
	ckptMu    sync.Mutex
	ckptRepos map[string]*checkpointRepo

	// mu guards env.
	mu sync.Mutex
	// env holds the KEY=VALUE entries the harness set with PUT /environment,
	// added to every process the daemon starts after that.
	env []string
}

// New builds a Daemon serving the computer at opts.Root. It fails when the
// token is empty or the root is not an existing directory, so a sandbox can
// never come up unauthenticated or pointed at nothing.
func New(opts Options, log *slog.Logger) (*Daemon, error) {
	if opts.Token == "" {
		return nil, errors.New("build daemon: token is empty")
	}
	if opts.Root == "" {
		return nil, errors.New("build daemon: root is empty")
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %s: %w", opts.Root, err)
	}
	// The root is canonicalised once so that confinement compares real paths.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %s: %w", opts.Root, err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat root %s: %w", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("root %s is not a directory", root)
	}

	d := &Daemon{
		root:    root,
		token:   opts.Token,
		log:     log,
		maxRead: opts.MaxReadBytes,
		mux:     http.NewServeMux(),

		ckptDir:   opts.CheckpointDir,
		ckptRepos: map[string]*checkpointRepo{},
	}
	if d.ckptDir == "" {
		d.ckptDir = DefaultCheckpointDir
	}
	if within(root, filepath.Clean(d.ckptDir)) {
		return nil, fmt.Errorf("build daemon: checkpoint directory %s is inside the root", d.ckptDir)
	}
	if d.maxRead <= 0 {
		d.maxRead = defaultMaxReadBytes
	}
	d.routes()
	return d, nil
}

// Root is the computer directory the daemon serves.
func (d *Daemon) Root() string { return d.root }

// Handler returns the daemon's root handler.
func (d *Daemon) Handler() http.Handler { return d.mux }

// routes registers every route the daemon serves. It is the one place to look
// for the shape of the sandbox API.
func (d *Daemon) routes() {
	d.mux.HandleFunc("GET /healthz", d.handleHealth)
	d.mux.HandleFunc("POST /exec", d.authed(d.handleExec))
	d.mux.HandleFunc("GET /process", d.authed(d.handleProcess))
	d.mux.HandleFunc("GET /files", d.authed(d.handleReadFile))
	d.mux.HandleFunc("PUT /files", d.authed(d.handleWriteFile))
	d.mux.HandleFunc("GET /stat", d.authed(d.handleStat))
	d.mux.HandleFunc("GET /list", d.authed(d.handleList))
	d.mux.HandleFunc("POST /checkpoint", d.authed(d.handleCheckpoint))
	d.mux.HandleFunc("PUT /environment", d.authed(d.handleSetEnvironment))
}

// handleHealth reports that the daemon is up. It is the only unauthenticated
// route, because it is the container health check.
func (d *Daemon) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, d.log, http.StatusOK, map[string]string{"status": "ok"})
}

// authed rejects a request that does not carry the daemon's bearer token.
func (d *Daemon) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(d.token)) != 1 {
			writeError(w, d.log, http.StatusUnauthorized, errors.New("invalid token"))
			return
		}
		h(w, r)
	}
}

// requestPath resolves the ?path= query parameter against the computer root.
func (d *Daemon) requestPath(r *http.Request) (string, error) {
	return d.resolve(r.URL.Query().Get("path"))
}

// rel renders an absolute path inside the root as a root-relative path, which
// is the only form that crosses the wire.
func (d *Daemon) rel(abs string) string {
	rel, err := filepath.Rel(d.root, abs)
	if err != nil {
		return filepath.Base(abs)
	}
	return rel
}

// fileInfo converts a stat result into the wire type.
func (d *Daemon) fileInfo(abs string, fi fs.FileInfo) FileInfo {
	return FileInfo{
		Name:    fi.Name(),
		Path:    d.rel(abs),
		Size:    fi.Size(),
		Mode:    uint32(fi.Mode()),
		ModTime: fi.ModTime().UTC(),
		IsDir:   fi.IsDir(),
	}
}

// writeJSON writes v as a JSON response. An encoding failure is logged rather
// than returned because the status line has already been sent. It mirrors
// the server package's writeJSON; the daemon cannot import that package.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error("write json response", "error", err)
	}
}

// writeError reports err to the caller with the given status code. The body
// is the daemon's flat ErrorResponse, not the harness API's error shape:
// only executor/sandbox reads it.
func writeError(w http.ResponseWriter, log *slog.Logger, status int, err error) {
	writeJSON(w, log, status, ErrorResponse{Error: err.Error()})
}

// statusFor maps a path error onto an HTTP status code.
func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrOutsideRoot):
		return http.StatusForbidden
	case errors.Is(err, fs.ErrNotExist):
		return http.StatusNotFound
	case errors.Is(err, fs.ErrPermission):
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
}
