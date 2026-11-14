// Package agentdef defines the built-in agents and their embedded prompts.
// It uses only the standard library and does no I/O at runtime.
// Builtins and Lookup return independent definitions; Override applies the
// user-editable class and system prompt. The server and store use these values.
package agentdef
