package server

import (
	"net/http"

	"github.com/erlidev/eika/internal/sentry"
)

type trustedSourceRequest struct {
	Host string `json:"host"`
}
type trustedSourcesResponse struct {
	Hosts []string `json:"hosts"`
}

func (s *Server) handleTrustedSources(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.deps.Store.TrustedSources(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, trustedSourcesResponse{Hosts: hosts})
}
func (s *Server) handleAddTrustedSource(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[trustedSourceRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setTrustedSource(w, r, req.Host, true)
}
func (s *Server) handleRemoveTrustedSource(w http.ResponseWriter, r *http.Request) {
	s.setTrustedSource(w, r, r.PathValue("host"), false)
}
func (s *Server) handleTrustWithheld(w http.ResponseWriter, r *http.Request) {
	row, err := s.deps.Store.Withheld(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host := sentry.SourceHost(row.Source)
	if host == "" {
		s.fail(w, r, invalidf("withheld source has no web host"))
		return
	}
	s.setTrustedSource(w, r, host, true)
}
func (s *Server) setTrustedSource(w http.ResponseWriter, r *http.Request, host string, trusted bool) {
	host = sentry.CanonicalHost(host)
	if host == "" {
		s.fail(w, r, invalidf("invalid host"))
		return
	}
	if err := s.deps.Store.SetTrustedSource(r.Context(), host, trusted); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
