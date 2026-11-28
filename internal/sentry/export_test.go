package sentry

import "context"

// Anchored exposes the anchored rule, which later packages call inside the gate.
func Anchored(ctx context.Context, g Gate, session, computer, host string, egress bool) (bool, error) {
	return g.(*gate).anchored(ctx, session, computer, host, egress)
}
