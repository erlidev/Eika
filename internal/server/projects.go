package server

import (
	"net/http"

	"github.com/erlidev/eika/internal/store"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.deps.Store.Projects(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := projectsResponse{Projects: make([]projectBody, 0, len(projects))}
	for _, p := range projects {
		out.Projects = append(out.Projects, projectView(p))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.deps.Store.Project(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, projectView(p))
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSON[*createProjectRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body == nil {
		s.fail(w, r, invalidf("project must be an object"))
		return
	}
	p, err := s.deps.Store.CreateProject(r.Context(), body.Slug, body.ProjectFields)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusCreated, projectView(p))
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSON[*store.ProjectUpdate](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body == nil {
		s.fail(w, r, invalidf("project update must be an object"))
		return
	}
	p, err := s.deps.Store.UpdateProject(r.Context(), r.PathValue("id"), *body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, projectView(p))
}
