package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
)

// DefaultRoot is where a workspace is mounted inside its sandbox.
const DefaultRoot = "/workspace"

// ErrNotFound reports that a path does not exist in the workspace. It remains
// as an alias for callers that used the sandbox-specific name.
var ErrNotFound = executor.ErrNotFound

// truncationNotice is appended to a command's stderr when the daemon dropped
// output because the command produced more than it streams.
const truncationNotice = "\n[eika: output truncated]\n"

// Options configures a Client.
type Options struct {
	// BaseURL is the daemon's address, for example http://eika-ws-1:7000.
	BaseURL string
	// Token is the workspace's eikad token.
	Token string
	// Root is the workspace root inside the sandbox. Empty means DefaultRoot.
	Root string
	// HTTPClient is used for every request. Nil means a client with no
	// overall timeout, because an exec streams for as long as it runs.
	HTTPClient *http.Client
}

// Client is an executor.Executor backed by one sandbox daemon.
type Client struct {
	base  string
	token string
	root  string
	http  *http.Client
}

// New builds a Client talking to the daemon at opts.BaseURL.
func New(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, errors.New("build sandbox client: base url is empty")
	}
	if opts.Token == "" {
		return nil, errors.New("build sandbox client: token is empty")
	}
	if _, err := url.Parse(opts.BaseURL); err != nil {
		return nil, fmt.Errorf("parse base url %s: %w", opts.BaseURL, err)
	}
	c := &Client{
		base:  strings.TrimSuffix(opts.BaseURL, "/"),
		token: opts.Token,
		root:  opts.Root,
		http:  opts.HTTPClient,
	}
	if c.root == "" {
		c.root = DefaultRoot
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	return c, nil
}

// Root is the absolute path of the workspace inside the sandbox.
func (c *Client) Root() string { return c.root }

// Exec runs a command in the sandbox, streaming its output into the spec's
// writers as the daemon produces it.
func (c *Client) Exec(ctx context.Context, spec executor.ExecSpec) (executor.ExecResult, error) {
	req := eikad.ExecRequest{
		Command:   spec.Command,
		Args:      spec.Args,
		Shell:     spec.Shell,
		Dir:       spec.Dir,
		Env:       spec.Env,
		TimeoutMS: spec.Timeout.Milliseconds(),
	}
	if spec.Stdin != nil {
		stdin, err := io.ReadAll(spec.Stdin)
		if err != nil {
			return executor.ExecResult{}, fmt.Errorf("read stdin: %w", err)
		}
		req.Stdin = stdin
	}
	body, err := json.Marshal(req)
	if err != nil {
		return executor.ExecResult{}, fmt.Errorf("encode exec request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/exec", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return executor.ExecResult{}, err
	}
	defer resp.Body.Close()

	return readFrames(resp.Body, spec.Stdout, spec.Stderr)
}

// readFrames consumes the newline-delimited frames of an /exec response.
func readFrames(r io.Reader, stdout, stderr io.Writer) (executor.ExecResult, error) {
	dec := json.NewDecoder(r)
	for {
		var frame eikad.ExecFrame
		if err := dec.Decode(&frame); err != nil {
			if errors.Is(err, io.EOF) {
				return executor.ExecResult{}, errors.New("exec stream ended without a result")
			}
			return executor.ExecResult{}, fmt.Errorf("decode exec frame: %w", err)
		}
		switch {
		case frame.Error != "":
			return executor.ExecResult{}, fmt.Errorf("exec: %s", frame.Error)
		case frame.ExitCode != nil:
			// The daemon drops output past its limit, so the caller is told in
			// the one place it is reading: the command's own stderr.
			if frame.Truncated && stderr != nil {
				fmt.Fprint(stderr, truncationNotice)
			}
			return executor.ExecResult{ExitCode: *frame.ExitCode, TimedOut: frame.TimedOut}, nil
		case frame.Stream == "stdout" && stdout != nil:
			if _, err := stdout.Write(frame.Data); err != nil {
				return executor.ExecResult{}, fmt.Errorf("write stdout: %w", err)
			}
		case frame.Stream == "stderr" && stderr != nil:
			if _, err := stderr.Write(frame.Data); err != nil {
				return executor.ExecResult{}, fmt.Errorf("write stderr: %w", err)
			}
		}
	}
}

// ReadFile returns the contents of path, truncated at opts.MaxBytes.
func (c *Client) ReadFile(ctx context.Context, path string, opts executor.ReadOpts) ([]byte, error) {
	q := url.Values{"path": {path}}
	if opts.MaxBytes > 0 {
		q.Set("max_bytes", strconv.FormatInt(opts.MaxBytes, 10))
	}
	resp, err := c.do(ctx, http.MethodGet, "/files", q, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// WriteFile writes data to path, creating parent directories.
func (c *Client) WriteFile(ctx context.Context, path string, data []byte) error {
	resp, err := c.do(ctx, http.MethodPut, "/files", url.Values{"path": {path}},
		bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// Stat describes one file in the workspace.
func (c *Client) Stat(ctx context.Context, path string) (executor.FileInfo, error) {
	var out eikad.FileInfo
	if err := c.getJSON(ctx, "/stat", url.Values{"path": {path}}, &out); err != nil {
		return executor.FileInfo{}, err
	}
	return fileInfo(out), nil
}

// List describes the direct children of a directory in the workspace.
func (c *Client) List(ctx context.Context, path string) ([]executor.FileInfo, error) {
	var out eikad.ListResponse
	if err := c.getJSON(ctx, "/list", url.Values{"path": {path}}, &out); err != nil {
		return nil, err
	}
	entries := make([]executor.FileInfo, len(out.Entries))
	for i, e := range out.Entries {
		entries[i] = fileInfo(e)
	}
	return entries, nil
}

// Terminal opens an interactive shell in the sandbox: a WebSocket on the
// daemon's /pty route carrying eikad.PTYMessage frames. A zero rows or cols
// leaves that dimension to the daemon's default. It is not part of
// executor.Executor on purpose: tools run commands, and a terminal is for the
// person using the workspace.
func (c *Client) Terminal(ctx context.Context, rows, cols uint16) (*websocket.Conn, error) {
	q := url.Values{}
	if rows > 0 {
		q.Set("rows", strconv.FormatUint(uint64(rows), 10))
	}
	if cols > 0 {
		q.Set("cols", strconv.FormatUint(uint64(cols), 10))
	}
	u := c.base + "/pty"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
	})
	if err != nil {
		// A refused handshake keeps the daemon's status and message.
		if resp != nil && resp.Body != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return nil, responseError(http.MethodGet, "/pty", resp)
		}
		return nil, fmt.Errorf("dial terminal: %w", err)
	}
	return conn, nil
}

// fileInfo converts the wire type into the executor type.
func fileInfo(f eikad.FileInfo) executor.FileInfo {
	return executor.FileInfo{
		Name:    f.Name,
		Path:    f.Path,
		Size:    f.Size,
		Mode:    fs.FileMode(f.Mode),
		ModTime: f.ModTime.UTC(),
		IsDir:   f.IsDir,
	}
}

// getJSON performs a GET and decodes the response into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, q, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

// do sends one request and turns a non-2xx response into an error. The caller
// closes the returned body.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, responseError(method, path, resp)
	}
	return resp, nil
}

// responseError builds the error for a failed request, mapping 404 onto
// ErrNotFound and 403 onto fs.ErrPermission so that callers can branch on a
// missing or refused file.
func responseError(method, path string, resp *http.Response) error {
	var body eikad.ErrorResponse
	// The message is best effort: a daemon that failed mid-response may not
	// have written a JSON body at all.
	_ = json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&body)
	msg := body.Error
	if msg == "" {
		msg = resp.Status
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%s %s: %s: %w", method, path, msg, ErrNotFound)
	case http.StatusForbidden:
		// The daemon refuses a path outside the root and a file it may not
		// open alike; both are a refusal, not a harness failure.
		return fmt.Errorf("%s %s: %s: %w", method, path, msg, fs.ErrPermission)
	}
	return fmt.Errorf("%s %s: %s", method, path, msg)
}

// Compile-time check that a Client is a usable executor.
var _ executor.Executor = (*Client)(nil)

// ProcessSpec is a long-lived process to run in the sandbox with its
// standard streams connected to the caller.
type ProcessSpec struct {
	Command string
	Args    []string
	// Dir is relative to the workspace root; empty is the root.
	Dir string
	// Env holds KEY=VALUE entries added to the sandbox's environment.
	Env []string
}

// maxStderrLine bounds one line of a process's stderr passed on; the rest
// of a longer line is dropped.
const maxStderrLine = 4096

// Process starts a long-lived process in the sandbox, on the daemon's
// /process route: writes reach its stdin, reads come from its stdout, and
// Close ends it. Each line it writes to stderr is passed to stderr, which
// may be nil. A read after the process ended fails with how it ended.
//
// Like Terminal, it is not part of executor.Executor: it is how the harness
// runs a stdio MCP server the user configured, not something a tool calls.
func (c *Client) Process(ctx context.Context, spec ProcessSpec, stderr func(string)) (io.ReadWriteCloser, error) {
	conn, resp, err := websocket.Dial(ctx, c.base+"/process", &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
	})
	if err != nil {
		if resp != nil && resp.Body != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return nil, responseError(http.MethodGet, "/process", resp)
		}
		return nil, fmt.Errorf("dial process: %w", err)
	}
	// Past the bound, the caller's reader has more than one whole message
	// to take; the daemon's output chunks are far smaller.
	conn.SetReadLimit(eikad.MaxProcessMessage)
	start, err := json.Marshal(eikad.ProcessMessage{Type: eikad.ProcessStart, Command: spec.Command, Args: spec.Args, Dir: spec.Dir, Env: spec.Env})
	if err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("encode process start: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, start); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("start process: %w", err)
	}
	p := &process{conn: conn, stderr: stderr, done: make(chan struct{})}
	p.out, p.feed = io.Pipe()
	p.ctx, p.stop = context.WithCancel(context.Background())
	go p.read()
	return p, nil
}

// process is the harness's end of a /process socket.
type process struct {
	conn   *websocket.Conn
	stderr func(string)
	// out is what Read returns, fed by read from the stdout messages.
	out  *io.PipeReader
	feed *io.PipeWriter
	// ctx lives as long as the process: Write has no context of its own,
	// and Close ends both a pending write and the reader.
	ctx  context.Context
	stop context.CancelFunc
	done chan struct{}
	// line holds the part of a stderr line still waiting for its end.
	line []byte
}

// Read returns what the process wrote to stdout.
func (p *process) Read(b []byte) (int, error) { return p.out.Read(b) }

// Write sends b to the process's stdin.
func (p *process) Write(b []byte) (int, error) {
	data, err := json.Marshal(eikad.ProcessMessage{Type: eikad.ProcessStdin, Data: b})
	if err != nil {
		return 0, err
	}
	if err := p.conn.Write(p.ctx, websocket.MessageText, data); err != nil {
		return 0, fmt.Errorf("write to process: %w", err)
	}
	return len(b), nil
}

// Close ends the process: the daemon closes its stdin, then stops it.
func (p *process) Close() error {
	// Output nobody will read must not hold up the reader.
	_ = p.out.Close()
	_ = p.conn.Close(websocket.StatusNormalClosure, "")
	p.stop()
	<-p.done
	return nil
}

// read delivers the daemon's messages until the socket ends.
func (p *process) read() {
	defer close(p.done)
	ended := errors.New("the process connection closed")
	defer func() { p.feed.CloseWithError(ended) }()
	for {
		_, data, err := p.conn.Read(p.ctx)
		if err != nil {
			return
		}
		var msg eikad.ProcessMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case eikad.ProcessStdout:
			if _, err := p.feed.Write(msg.Data); err != nil {
				return
			}
		case eikad.ProcessStderr:
			p.stderrLines(msg.Data)
		case eikad.ProcessExit:
			p.stderrLines([]byte("\n"))
			ended = fmt.Errorf("the process exited with status %d", msg.ExitCode)
		case eikad.ProcessError:
			ended = errors.New(msg.Error)
		}
	}
}

// stderrLines passes on each whole line of stderr.
func (p *process) stderrLines(data []byte) {
	if p.stderr == nil {
		return
	}
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if room := maxStderrLine - len(p.line); room > 0 {
				p.line = append(p.line, data[:min(room, len(data))]...)
			}
			return
		}
		if room := maxStderrLine - len(p.line); room > 0 {
			p.line = append(p.line, data[:min(room, i)]...)
		}
		if line := strings.TrimRight(string(p.line), "\r"); line != "" {
			p.stderr(line)
		}
		p.line, data = p.line[:0], data[i+1:]
	}
}
