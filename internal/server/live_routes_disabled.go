//go:build !live

package server

import "net/http"

func (s *Server) liveRoutes(_ *http.ServeMux) {}
