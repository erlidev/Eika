//go:build live

package server

import (
	"net"
	"net/http"
	"os"
	"syscall"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/netguard"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// liveRoutes supplies the live suite with background runs and lease times.
// It is absent from normal builds and uses the same authentication as the API.
func (s *Server) liveRoutes(api *http.ServeMux) {
	if !s.opts.LiveTest {
		return
	}
	if os.Getenv("EIKA_LIVE_PUSH_SINK") != "" {
		s.notifications.client = &http.Client{}
	}
	api.HandleFunc("POST /api/_live/sessions/{id}/background", func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeJSONWithin[struct {
			Text string `json:"text"`
		}](r, 64<<10)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		sess, err := s.deps.Store.Session(r.Context(), r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if sess.Kind != store.SessionRoutine {
			s.fail(w, r, conflictf("background test runs require a routine session"))
			return
		}
		run, _, err := s.runs.launch(r.Context(), r.PathValue("id"), "", job{message: provider.UserMessage(req.Text), priority: inference.Background})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, s.log, http.StatusAccepted, run)
	})
	api.HandleFunc("GET /api/_live/inference", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.log, http.StatusOK, s.queue(r.Context()).Snapshot())
	})
}

// liveProxyControl permits only the loopback fixture port for a live worker.
func liveProxyControl(enabled bool) func(string, string, syscall.RawConn) error {
	port := os.Getenv("EIKA_LIVE_PROXY_FIXTURE_PORT")
	if !enabled || port == "" {
		return nil
	}
	return func(network, address string, conn syscall.RawConn) error {
		host, gotPort, err := net.SplitHostPort(address)
		if err == nil && gotPort == port {
			ip := net.ParseIP(host)
			if ip != nil && ip.IsLoopback() {
				return nil
			}
		}
		return netguard.Control(network, address, conn)
	}
}
