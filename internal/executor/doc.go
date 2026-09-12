// Package executor defines the only way an agent touches a workspace.
//
// Tools never read a file, write a file, or start a process directly. They
// call an Executor, whose production implementation (executor/sandbox) talks
// to the eikad daemon inside a workspace container. Paths are relative to the
// executor's root and never escape it.
//
// The Executor interface is this package's extension point; implementations
// live in subpackages.
package executor
