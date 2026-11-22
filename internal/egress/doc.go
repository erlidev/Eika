// Package egress decides what a sandbox may reach on the network, and is the
// only way out on a host with egress control.
//
// A computer's egress Mode is public, allowlist, or none. A sandbox sits on an internal
// Docker network with no route out, and its processes are pointed at Proxy,
// an HTTP proxy in the harness that accepts CONNECT tunnels and plain http
// requests. The proxy knows a sandbox by its computer id and proxy token,
// sent as proxy credentials; it asks Options.Policy for the computer's mode
// and allowlist on every request, so a change applies to the next
// connection. It dials through netguard, so a sandbox never reaches the
// database, the search service, or another container through it.
//
// Allowed matches a host against allowlist patterns, and ValidPattern checks
// one before it is stored. Proxy.Blocked lists the hosts a computer was
// refused lately, which the UI offers to allow. It depends on netguard and
// the standard library.
package egress
