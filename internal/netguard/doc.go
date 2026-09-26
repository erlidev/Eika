// Package netguard keeps the harness's outbound connections on the public
// internet.
//
// The harness shares a Docker network with postgres, searxng, and every
// sandbox, so a connection it makes on somebody else's behalf, to a URL the
// model chose or a host a sandbox asked for, must not land on one of them.
// Control is a net.Dialer Control function that refuses an address that is
// not public; it runs on the resolved address, so a public name that
// resolves into a private range is refused too. search/fetch and the egress
// proxy dial through it. It depends on the standard library alone.
package netguard
