// Package workspace runs the sandbox containers agents work in.
//
// A workspace is one container plus the volume holding its files,
// created from a Spec and addressed by its id. Host wraps the Docker
// Engine client and owns the whole lifecycle: create, start, stop, destroy,
// list, and inspect. Host.Executor hands back an executor.Executor pointed at
// the workspace's eikad daemon, which is the only way an agent reaches the
// container. Host.Terminal opens a shell there for the person using the
// workspace, and Host.Process starts a stdio MCP server there for the
// harness; both are kept off the executor so no tool can reach them.
//
// A Spec's Confinement sets the container's CPU, memory, and process limits
// and puts it on the open sandbox network or the internal one, whose only way
// out is the harness's egress proxy; Host.Confine changes both on an existing
// container without restarting it, and Start gives eikad the proxy settings
// a proxied container's processes need. Host.Usage samples what a container
// consumes, and Host.Capacity what the Docker host has.
//
// Containers carry the label eika.workspace=<id>, so List reconciles the
// harness with what is actually running after a restart. Only server imports
// this package; tools never do.
package workspace
