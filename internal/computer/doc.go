// Package computer runs the sandbox containers agents work in.
//
// A computer is one container plus the volume holding its files,
// created from a Spec and addressed by its id. Host wraps the Docker
// Engine client and owns the whole lifecycle: create, start, stop, destroy,
// list, and inspect. Host.Executor hands back an executor.Executor pointed at
// the computer's eikad daemon, which is the only way an agent reaches the
// container. Host.Process starts a stdio MCP server there for the harness; it
// is kept off the executor so no tool can hold a process of its own.
//
// A Spec's Confinement sets the container's CPU, memory, and process limits
// and puts it on the open sandbox network or the internal one, whose only way
// out is the harness's egress proxy; Host.Confine changes both on an existing
// container without restarting it, and Start gives eikad the proxy settings
// a proxied container's processes need. Host.Capacity reports what the Docker
// host has.
//
// Containers carry the label eika.computer=<id>, so List reconciles the
// harness with what is actually running after a restart. Only server imports
// this package; tools never do.
package computer
