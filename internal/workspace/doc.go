// Package workspace runs the sandbox containers agents work in.
//
// A workspace is one container plus the volume holding its clone of a
// project, created from a Spec and addressed by its id. Host wraps the Docker
// Engine client and owns the whole lifecycle: create, start, stop, destroy,
// list, and inspect. Host.Executor hands back an executor.Executor pointed at
// the workspace's eikad daemon, which is the only way an agent reaches the
// container.
//
// Containers carry the label eika.workspace=<id>, so List reconciles the
// harness with what is actually running after a restart. Repositories come
// from the hub subpackage. Only server and subagent import this package;
// tools never do.
package workspace
