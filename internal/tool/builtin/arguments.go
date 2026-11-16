package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// The strict argument decoder of the file, project, subagent, ask and report
// tools. It reads the raw bytes token by token, because decoding them into a
// map or struct would silently replace invalid UTF-8 and unpaired surrogates
// with U+FFFD, take the last of duplicate keys, and ignore unknown ones.

// stringArguments decodes a call's arguments as an object of strings whose
// keys are allowed, each at most once, with every required key present.
func stringArguments(raw []byte, allowed map[string]json.RawMessage, required []string) (map[string]string, error) {
	fields, err := objectArguments(raw, allowed, required)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(fields))
	for key, v := range fields {
		s, err := stringArgument(key, v)
		if err != nil {
			return nil, err
		}
		out[key] = s
	}
	return out, nil
}

// objectArguments splits a call's arguments into the raw values of an object
// whose keys are allowed, each at most once. Arguments that are not valid
// UTF-8 are refused here: decoding would replace the bad bytes silently.
func objectArguments(raw []byte, allowed map[string]json.RawMessage, required []string) (map[string]json.RawMessage, error) {
	if len(raw) > maxFileArgsBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("arguments must be valid UTF-8 and at most %d bytes", maxFileArgsBytes)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("arguments must be an object")
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("decode argument name: %w", err)
		}
		key, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("invalid argument")
		}
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("unknown argument %q", key)
		}
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("duplicate argument %q", key)
		}
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			return nil, fmt.Errorf("decode %s: %w", key, err)
		}
		out[key] = v
	}
	if _, err := d.Token(); err != nil {
		return nil, fmt.Errorf("decode arguments: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after arguments")
	}
	for _, k := range required {
		if _, ok := out[k]; !ok {
			return nil, fmt.Errorf("%s is required", k)
		}
	}
	return out, nil
}

// stringArgument decodes one argument that must be a string without an
// unpaired surrogate escape, which decoding would turn into U+FFFD.
func stringArgument(key string, v json.RawMessage) (string, error) {
	var s *string
	if json.Unmarshal(v, &s) != nil || s == nil {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if err := validStringEscapes(v); err != nil {
		return "", err
	}
	return *s, nil
}

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
