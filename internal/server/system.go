package server

import (
	"context"
	"net/http"
	"time"
)

// systemCheckTimeout bounds how long GET /api/system waits for the Docker
// daemon, so a socket that hangs shows up as a failed check rather than a
// request that never ends.
const systemCheckTimeout = 5 * time.Second

// systemResponse is the body of GET /api/system: what the setup screens check
// before the first workspace is made.
type systemResponse struct {
	Docker       dockerStatus `json:"docker"`
	SandboxImage imageStatus  `json:"sandbox_image"`
	Providers    int          `json:"providers"`
	Models       int          `json:"models"`
	Projects     int          `json:"projects"`
}

// dockerStatus says whether the harness can use the Docker socket.
type dockerStatus struct {
	Reachable bool `json:"reachable"`
	// Error is the daemon client's reason when it is not reachable.
	Error string `json:"error,omitempty"`
}

// imageStatus says whether the sandbox image new workspaces run is there.
type imageStatus struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
}

// handleSystem reports whether the harness is ready to run workspaces and how
// much of it the user has set up.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := systemResponse{SandboxImage: imageStatus{Name: s.sandboxImage(ctx)}}

	checkCtx, cancel := context.WithTimeout(ctx, systemCheckTimeout)
	present, err := s.deps.Workspaces.HasImage(checkCtx, body.SandboxImage.Name)
	cancel()
	if err != nil {
		body.Docker.Error = err.Error()
	} else {
		body.Docker.Reachable = true
		body.SandboxImage.Present = present
	}

	providers, err := s.deps.Store.Providers(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	models, err := s.deps.Store.Models(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	projects, err := s.deps.Store.Projects(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body.Providers, body.Models, body.Projects = len(providers), len(models), len(projects)
	writeJSON(w, s.log, http.StatusOK, body)
}
