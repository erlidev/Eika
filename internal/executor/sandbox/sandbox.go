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

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
)

// DefaultRoot is where a workspace is mounted inside its sandbox.
const DefaultRoot = "/workspace"

// ErrNotFound reports that a path does not exist in the workspace.
var ErrNotFound = errors.New("file not found")

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
// ErrNotFound so that callers can branch on a missing file.
func responseError(method, path string, resp *http.Response) error {
	var body eikad.ErrorResponse
	// The message is best effort: a daemon that failed mid-response may not
	// have written a JSON body at all.
	_ = json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&body)
	msg := body.Error
	if msg == "" {
		msg = resp.Status
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s: %s: %w", method, path, msg, ErrNotFound)
	}
	return fmt.Errorf("%s %s: %s", method, path, msg)
}

// Compile-time check that a Client is a usable executor.
var _ executor.Executor = (*Client)(nil)
