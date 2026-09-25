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
