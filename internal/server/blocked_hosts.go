package server

import (
	"net/http"

	"github.com/erlidev/eika/internal/sentry"
)

type blockedHostRequest struct {
	Host string `json:"host"`
}
type blockedHostsResponse struct {
	Hosts []string `json:"hosts"`
}

func (s *Server) handleBlockedHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.deps.Store.BlockedHosts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, blockedHostsResponse{Hosts: hosts})
}
func (s *Server) handleAddBlockedHost(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[blockedHostRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setBlockedHost(w, r, req.Host, true)
}
func (s *Server) handleRemoveBlockedHost(w http.ResponseWriter, r *http.Request) {
	s.setBlockedHost(w, r, r.PathValue("host"), false)
}
func (s *Server) setBlockedHost(w http.ResponseWriter, r *http.Request, host string, blocked bool) {
	host = sentry.EgressHost(host)
	if host == "" {
		s.fail(w, r, invalidf("invalid host"))
		return
	}
	if err := s.deps.Store.SetBlockedHost(r.Context(), host, blocked); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
