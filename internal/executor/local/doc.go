//go:build !prod

// Package local implements executor.Executor against a directory on the
// harness filesystem.
//
// It exists so that tools and the agent loop can be tested without Docker. It
// is excluded from production builds by the prod build tag, because agent
// actions in a deployment must run inside a sandbox.
//
// The entry point is New, which takes the directory that becomes the workspace
// root. Every path is resolved with executor.Resolve and re-checked after
// symlink evaluation, so nothing outside the root is reachable. A command runs
// in its own process group, so a timeout kills its background children too.
// That needs Unix process groups, which is all Eika runs on.
package local
