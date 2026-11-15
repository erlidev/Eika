package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// fileTool provides bounded text operations through the call's executor.
type fileTool struct{ name string }

// Name identifies the file operation.
func (t fileTool) Name() string { return t.name }

// Description states the operation and its text limits.
func (t fileTool) Description() string {
	switch t.name {
	case "file_read":
		return "Read a UTF-8 text file of at most 32768 bytes in the working directory. Use bash for larger files or binary data."
	case "file_write":
		return "Create or replace a UTF-8 text file of at most 1048576 bytes in the working directory. Creates parent directories. An empty content string creates an empty file."
	default:
		return "Replace exactly one occurrence of old_text with new_text in a UTF-8 file in the working directory. Read the file first. Both the original and result must be at most 1048576 bytes. No match or multiple matches leave the file unchanged. An empty new_text deletes the match."
	}
}

func (t fileTool) fields() []string {
	switch t.name {
	case "file_read":
		return []string{"path"}
	case "file_write":
		return []string{"path", "content"}
	default:
		return []string{"path", "old_text", "new_text"}
	}
}

// Schema lists the required string arguments.
func (t fileTool) Schema() json.RawMessage {
	properties := make(map[string]any)
	for _, field := range t.fields() {
		properties[field] = map[string]string{"type": "string"}
	}
	b, _ := json.Marshal(map[string]any{
		"type": "object", "properties": properties, "required": t.fields(), "additionalProperties": false,
	})
	return b
}

// Call validates the arguments and uses the computer executor.
func (t fileTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := t.arguments(raw)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if c.Exec == nil {
		return tool.Errorf("%s: a computer is required", t.name), nil
	}
	if err := ctx.Err(); err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	ctx, cancel := context.WithTimeout(ctx, defaultBashTimeout)
	defer cancel()
	path := args["path"]
	if _, err := executor.Resolve(c.Exec.Root(), path); err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	var data []byte
	switch t.name {
	case "file_read":
		data, err = readTextFile(ctx, c.Exec, path, maxOutputBytes)
		if err == nil {
			if len(data) == 0 {
				return tool.Text("(empty file)"), nil
			}
			return tool.Text(string(data)), nil
		}
	case "file_write":
		data = []byte(args["content"])
	case "file_edit":
		data, err = readTextFile(ctx, c.Exec, path, maxFileBytes)
		if err == nil {
			old := args["old_text"]
			// Search again one byte later to count overlapping matches too.
			at := bytes.Index(data, []byte(old))
			switch {
			case at < 0:
				err = fmt.Errorf("old_text was not found")
			case bytes.Contains(data[at+1:], []byte(old)):
				err = fmt.Errorf("old_text matches more than once; include more context")
			case len(data)-len(old)+len(args["new_text"]) > maxFileBytes:
				err = fmt.Errorf("result exceeds %d bytes", maxFileBytes)
			default:
				data = []byte(strings.Replace(string(data), old, args["new_text"], 1))
			}
		}
	}
	if err == nil {
		err = c.Exec.WriteFile(ctx, path, data)
	}
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	return tool.Text(fmt.Sprintf("%s: wrote %d bytes", t.name, len(data))), nil
}

// arguments validates the raw bytes before JSON decoding can replace bad UTF-8.
func (t fileTool) arguments(raw json.RawMessage) (map[string]string, error) {
	if len(raw) > maxFileArgsBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("arguments must be valid UTF-8 and at most %d bytes", maxFileArgsBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("arguments must be an object")
	}
	args := make(map[string]string)
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("decode argument name: %w", err)
		}
		name, ok := key.(string)
		if !ok || !slices.Contains(t.fields(), name) {
			return nil, fmt.Errorf("unknown argument %q", key)
		}
		if _, exists := args[name]; exists {
			return nil, fmt.Errorf("duplicate argument %q", name)
		}
		var encoded json.RawMessage
		if err := dec.Decode(&encoded); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		var value *string
		if err := json.Unmarshal(encoded, &value); err != nil || value == nil {
			return nil, fmt.Errorf("%s must be a string", name)
		}
		// Check unpaired UTF-16 escapes in the original encoded bytes.
		if err := validStringEscapes(encoded); err != nil {
			return nil, err
		}
		if len(*value) > maxFileBytes || strings.ContainsRune(*value, 0) {
			return nil, fmt.Errorf("%s must have no NUL and at most %d bytes", name, maxFileBytes)
		}
		args[name] = *value
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("decode arguments: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after arguments")
	}
	for _, field := range t.fields() {
		if _, ok := args[field]; !ok {
			return nil, fmt.Errorf("%s is required", field)
		}
	}
	if strings.TrimSpace(args["path"]) == "" || strings.TrimSpace(args["path"]) != args["path"] || len(args["path"]) > maxFilePathBytes {
		return nil, fmt.Errorf("path must have 1 to %d bytes and no surrounding whitespace", maxFilePathBytes)
	}
	if t.name == "file_edit" && args["old_text"] == "" {
		return nil, fmt.Errorf("old_text must not be empty")
	}
	return args, nil
}

func readTextFile(ctx context.Context, e executor.Executor, path string, limit int) ([]byte, error) {
	info, err := e.Stat(ctx, path)
	if err != nil {
		return nil, err
	}
	if !info.Mode.IsRegular() {
		return nil, fmt.Errorf("path must name a regular file")
	}
	if info.Size > int64(limit) {
		return nil, fmt.Errorf("file exceeds %d bytes; use bash for larger files", limit)
	}
	data, err := e.ReadFile(ctx, path, executor.ReadOpts{MaxBytes: int64(limit + 1)})
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	if int64(len(data)) != info.Size {
		return nil, fmt.Errorf("file size changed or read was incomplete; read again before editing")
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return nil, fmt.Errorf("file must be UTF-8 text without NUL")
	}
	return data, nil
}

var _ tool.Tool = fileTool{}

// validStringEscapes rejects the unpaired surrogate escapes JSON permits but
// UTF-8 cannot represent. JSON syntax has already been checked by the decoder.
func validStringEscapes(raw []byte) error {
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid Unicode escape: %w", err)
		}
		i += 4
		if n < 0xd800 || n > 0xdfff {
			continue
		}
		if n > 0xdbff || i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return fmt.Errorf("unpaired Unicode surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired Unicode surrogate")
		}
		i += 6
	}
	return nil
}
