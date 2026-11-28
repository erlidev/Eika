package sentry

import "context"

// Anchored exposes the anchored rule, which later packages call inside the gate.
func Anchored(ctx context.Context, g Gate, session, computer, host string, egress bool) (bool, error) {
	return g.(*gate).anchored(ctx, session, computer, host, egress)
}

// EgressCountOf exposes a run's counts for a host.
func EgressCountOf(g Gate, run, host string) (connections int, sent, received int64) {
	c := g.(*gate).egressCount(run, host)
	return c.Connections, c.Sent, c.Received
}

// OutboundText exposes what a call sends.
func OutboundText(name, args string) []string {
	return outbound(Action{Kind: KindTool, Name: name, Args: []byte(args)})
}
