// Package tool defines what an agent can do and the registry that holds it.
//
// A Tool describes itself to the model with a name, a description, and a JSON
// Schema for its parameters, and runs one call against a CallContext. The
// context carries the executor the tool must use for every file and process
// action, plus the event emitter it streams partial output through, so a tool
// never touches the harness filesystem and never imports the workspace
// package.
//
// The entry points are Tool, Registry, and CallContext. The built-in tools
// live in tool/builtin, which also holds the registry of them.
package tool
