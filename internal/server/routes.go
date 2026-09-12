package server

import (
	"net/http"
	"os"
	"path/filepath"
)

// routes registers every route the server serves. It is the one place to look
// for the shape of the HTTP surface.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/healthz", s.handleHealth)
	if s.opts.WebDir != "" {
		s.mux.Handle("GET /", s.handleWeb(s.opts.WebDir))
	}
}

// health is the body of a health check response.
type health struct {
	Status string `json:"status"`
}

// handleHealth reports that the process is up. It is the only route that never
// requires authentication.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, health{Status: "ok"})
}

// handleWeb serves the built frontend, falling back to index.html so that
// client-side routes resolve on a full page load.
func (s *Server) handleWeb(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anything that is not a regular file is a client-side route, not a
		// static asset. Directories included: the UI never wants a listing.
		fi, err := os.Stat(filepath.Join(dir, filepath.Clean(r.URL.Path)))
		if err != nil || fi.IsDir() {
			http.ServeFile(w, r, index)
			return
		}
		files.ServeHTTP(w, r)
	})
}
