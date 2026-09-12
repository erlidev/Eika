package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decodeArgs unmarshals a tool call's arguments. Empty arguments decode to the
// zero value, which is what a call with no parameters sends.
func decodeArgs[T any](raw json.RawMessage) (T, error) {
	var v T
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return v, nil
	}
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return v, fmt.Errorf("decode arguments: %w", err)
	}
	return v, nil
}

// pathOrDot returns the path a tool was given, defaulting to the workspace
// root.
func pathOrDot(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// limitOr returns the requested limit, or fallback when it is not positive.
func limitOr(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	return limit
}
