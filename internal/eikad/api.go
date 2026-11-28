package eikad

import "time"

// BinaryPath is where eikad lives in every sandbox. The harness copies its
// own build there, and runs `eikad filter` from it for fetch filters.
const BinaryPath = "/usr/local/bin/eikad"

// DefaultRoot is the computer root inside a sandbox: the home directory of
// the user every command runs as, and where a computer's volume is mounted.
// It is the one copy of the path in Go; the sandbox image's Dockerfile
// spells it too, and a test keeps the two equal.
const DefaultRoot = "/home/eika"

// DefaultCheckpointDir is where a sandbox keeps the shadow repositories of
// project trees. It is outside the computer root, so no path a file tool can
// name reaches it, and the sandbox image creates it for the daemon's user.
const DefaultCheckpointDir = "/var/lib/eikad/checkpoints"

// ExecRequest is the body of POST /exec.
//
// Stdin is sent up front rather than streamed: /exec is for non-interactive
// commands, and a long-lived process with streams belongs on /process.
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
	// Truncated reports that the command produced more output than the daemon
	// streams, and the rest was dropped.
	Truncated bool `json:"truncated,omitempty"`
	// Error describes a failure to run the command at all.
	Error string `json:"error,omitempty"`
}

// EnvironmentRequest is the body of PUT /environment.
type EnvironmentRequest struct {
	// Env holds the KEY=VALUE entries added to the environment of every
	// process the daemon starts from now on. It replaces what an earlier
	// request set; empty clears it.
	Env []string `json:"env"`
}

// FileInfo describes one file. Path is relative to the computer root and Mode
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

// Process message types, used on GET /process.
const (
	// ProcessStart is the client's first message, naming the command.
	ProcessStart = "start"
	// ProcessStdin carries bytes for the process's standard input.
	ProcessStdin = "stdin"
	// ProcessStdout and ProcessStderr carry the process's output.
	ProcessStdout = "stdout"
	ProcessStderr = "stderr"
	// ProcessExit is the daemon's last message when the process ran.
	ProcessExit = "exit"
	// ProcessError is the daemon's last message when it could not run the
	// process at all.
	ProcessError = "error"
)

// ProcessMessage is one message on a /process WebSocket. The client sends a
// start message, then stdin messages; the daemon sends stdout and stderr
// messages, then one exit or error message.
type ProcessMessage struct {
	// Type is one of the Process constants.
	Type string `json:"type"`
	// Command, Args, Dir, and Env describe the process on a start message.
	// Dir is relative to the root; Env holds KEY=VALUE entries added to
	// the daemon's environment.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	Env     []string `json:"env,omitempty"`
	// Data is input or output, base64 encoded on the wire.
	Data []byte `json:"data,omitempty"`
	// ExitCode is the process's exit status on an exit message.
	ExitCode int `json:"exit_code,omitempty"`
	// Error says why the process could not run, on an error message.
	Error string `json:"error,omitempty"`
}

// The labels of a checkpoint: the two moments of a run it is taken at.
const (
	CheckpointStart = "start"
	CheckpointEnd   = "end"
)

// CheckpointRequest is the body of POST /checkpoint.
type CheckpointRequest struct {
	// Dir is the project tree, relative to the root. It is never the root.
	Dir string `json:"dir"`
	// Run is the id of the run the checkpoint belongs to.
	Run string `json:"run"`
	// Label is CheckpointStart or CheckpointEnd.
	Label string `json:"label"`
}

// CheckpointResponse is the body of a successful POST /checkpoint.
type CheckpointResponse struct {
	// Commit is the id of the commit the tree was recorded in.
	Commit string `json:"commit"`
	// Files is how many changed paths the commit holds, deletions included.
	Files int `json:"files"`
	// Skipped is how many changed files were left out for being over 10 MB.
	Skipped int `json:"skipped"`
	// TookMS is how long the checkpoint took, in milliseconds.
	TookMS int64 `json:"took_ms"`
}
