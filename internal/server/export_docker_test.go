//go:build docker

package server

import (
	"context"
	"time"
)

// SweepIdle runs one idle sweep as though it were now, which lets a test
// move the clock instead of waiting out the timeout.
func (s *Server) SweepIdle(ctx context.Context, now time.Time) { s.idle.sweep(ctx, now) }
