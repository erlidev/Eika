//go:build !live

package server

import (
	"net/http"
	"syscall"
)

func liveProxyControl(bool) func(string, string, syscall.RawConn) error { return nil }

func (s *Server) liveRoutes(_ *http.ServeMux) {}
