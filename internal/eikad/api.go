package eikad

import "time"

// ExecRequest is the body of POST /exec.
//
// Stdin is sent up front rather than streamed: /exec is for non-interactive
// commands, and an interactive session belongs on /pty.
type ExecRequest struct {
	// Command is the program to run, or the script when Shell is set.
	Command string `json:"command"`
	// Args are the arguments to Command. Ignored when Shell is set.
	Args []string `json:"args,omitempty"`
	// Shell runs Command through `sh -c`.
	Shell bool `json:"shell,omitempty"`
	// Dir is the working directory relative to the root. Empty means root.
	Dir string `json:"dir,omitempty"`
	// Env holds additional KEY=VALUE entries for the command.
	Env []string `json:"env,omitempty"`
	// TimeoutMS kills the command after this many milliseconds. Zero means no
	// limit beyond the request's own lifetime.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// Stdin is fed to the command, base64 encoded on the wire.
	Stdin []byte `json:"stdin,omitempty"`
}

// ExecFrame is one newline-delimited JSON frame of an /exec response. Output
// frames carry Stream and Data; the last frame of a run carries ExitCode, or
// Error if the command could not be started.
type ExecFrame struct {
	// Stream is "stdout" or "stderr" on an output frame.
	Stream string `json:"stream,omitempty"`
	// Data is the chunk of output, base64 encoded on the wire.
	Data []byte `json:"data,omitempty"`
	// ExitCode is set on the final frame of a command that ran.
	ExitCode *int `json:"exit_code,omitempty"`
	// TimedOut reports that the command was killed by its timeout.
	TimedOut bool `json:"timed_out,omitempty"`
	// Error describes a failure to run the command at all.
	Error string `json:"error,omitempty"`
}

// FileInfo describes one file. Path is relative to the workspace root and Mode
// is the Go file mode bits.
type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

// ListResponse is the body of GET /list.
type ListResponse struct {
	Entries []FileInfo `json:"entries"`
}

// ErrorResponse is the body of every failed request.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Change kinds reported by the /watch stream.
const (
	ChangeCreated  = "created"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
)

// WatchEvent is one change reported by GET /watch.
type WatchEvent struct {
	// Path is the changed file, relative to the workspace root.
	Path string `json:"path"`
	// Kind is one of the Change constants.
	Kind string `json:"kind"`
	// IsDir reports whether the changed file is a directory.
	IsDir bool `json:"is_dir"`
	// Time is when the daemon observed the change, in UTC.
	Time time.Time `json:"time"`
}

// PTY message types, used in both directions on GET /pty.
const (
	PTYInput  = "input"
	PTYOutput = "output"
	PTYResize = "resize"
	PTYExit   = "exit"
)

// PTYMessage is one message on a /pty WebSocket. The client sends input and
// resize messages; the daemon sends output and a final exit message.
type PTYMessage struct {
	// Type is one of the PTY constants.
	Type string `json:"type"`
	// Data is terminal input or output, base64 encoded on the wire.
	Data []byte `json:"data,omitempty"`
	// Rows and Cols are the new window size on a resize message.
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	// ExitCode is the shell's exit status on an exit message.
	ExitCode int `json:"exit_code,omitempty"`
}
