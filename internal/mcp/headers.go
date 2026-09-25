package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// headerValue renders a value for an Mcp-Name or Mcp-Param-* header: as it
// is when it is plain visible ASCII, and in the base64 sentinel form when it
// is not, or when it would read as one.
func headerValue(v string) string {
	if plainHeaderValue(v) && !(strings.HasPrefix(v, "=?base64?") && strings.HasSuffix(v, "?=")) {
		return v
	}
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(v)) + "?="
}

// plainHeaderValue reports whether v can travel in a header unencoded:
// visible ASCII, spaces, and tabs, with no space at either end.
func plainHeaderValue(v string) bool {
	if v == "" || v != strings.TrimSpace(v) {
		return v == ""
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < 0x20 || c > 0x7e) && c != '\t' {
			return false
		}
	}
	return true
}

// headerParam is one tool argument a server asks to see mirrored into an
// Mcp-Param-* header, and where in the arguments it lives.
type headerParam struct {
	// Name is the header's name after "Mcp-Param-".
	Name string
	// Path is the chain of property names from the schema root.
	Path []string
}

// headerParams reads the x-mcp-header annotations of a tool's input schema.
// A tool whose annotations break the rules of the 2026-07-28 revision is
// excluded from the tools a Streamable HTTP client offers; the error says
// why.
func headerParams(schema json.RawMessage) ([]headerParam, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, fmt.Errorf("the input schema is not an object: %w", err)
	}
	var out []headerParam
	seen := map[string]bool{}
	if err := collectHeaderParams(root, nil, true, &out, seen); err != nil {
		return nil, err
	}
	return out, nil
}

// collectHeaderParams walks a schema looking for x-mcp-header. reachable is
// true while every step from the root has been a properties key; an
// annotation anywhere else makes the tool invalid.
func collectHeaderParams(schema map[string]json.RawMessage, path []string, reachable bool, out *[]headerParam, seen map[string]bool) error {
	if raw, ok := schema["x-mcp-header"]; ok && len(path) > 0 {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return fmt.Errorf("x-mcp-header on %s is not a string", strings.Join(path, "."))
		}
		if !reachable {
			return fmt.Errorf("x-mcp-header %q is not reachable from the schema root through properties alone", name)
		}
		if !headerToken(name) {
			return fmt.Errorf("x-mcp-header %q is not a valid HTTP header name", name)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("x-mcp-header %q is used twice", name)
		}
		var typ string
		_ = json.Unmarshal(schema["type"], &typ)
		switch typ {
		case "string", "integer", "boolean":
		default:
			return fmt.Errorf("x-mcp-header %q is on a parameter that is not a string, integer, or boolean", name)
		}
		seen[strings.ToLower(name)] = true
		*out = append(*out, headerParam{Name: name, Path: append([]string(nil), path...)})
	}
	for key, raw := range schema {
		switch key {
		case "properties":
			var props map[string]json.RawMessage
			if json.Unmarshal(raw, &props) != nil {
				continue
			}
			for name, sub := range props {
				var child map[string]json.RawMessage
				if json.Unmarshal(sub, &child) != nil {
					continue
				}
				if err := collectHeaderParams(child, append(path, name), reachable, out, seen); err != nil {
					return err
				}
			}
		case "items", "prefixItems", "additionalProperties", "patternProperties", "anyOf", "oneOf", "allOf",
			"not", "if", "then", "else", "$defs", "definitions", "dependentSchemas", "contains":
			// Anything below these is not statically reachable, so an
			// annotation there is an error rather than a header.
			if err := forEachSubschema(raw, func(sub map[string]json.RawMessage) error {
				return collectHeaderParams(sub, append(path, key), false, out, seen)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// forEachSubschema calls fn for every schema object found in raw: an object,
// an array of them, or a map of them.
func forEachSubschema(raw json.RawMessage, fn func(map[string]json.RawMessage) error) error {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		if _, isSchema := obj["type"]; isSchema || hasSchemaKey(obj) {
			return fn(obj)
		}
		for _, v := range obj {
			if err := forEachSubschema(v, fn); err != nil {
				return err
			}
		}
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		for _, v := range list {
			if err := forEachSubschema(v, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// hasSchemaKey reports whether obj looks like a schema rather than a map of
// schemas keyed by name.
func hasSchemaKey(obj map[string]json.RawMessage) bool {
	for _, k := range []string{"properties", "items", "x-mcp-header", "anyOf", "oneOf", "allOf", "$ref", "enum", "const"} {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

// headerToken reports whether name is an RFC 9110 token.
func headerToken(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// paramHeaders reads the values the annotated parameters have in a call's
// arguments. A parameter that is absent or null sends no header.
func paramHeaders(params []headerParam, args json.RawMessage) map[string]string {
	if len(params) == 0 {
		return nil
	}
	var root any
	if json.Unmarshal(args, &root) != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range params {
		v := root
		for _, key := range p.Path {
			obj, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = obj[key]
		}
		switch value := v.(type) {
		case string:
			out[p.Name] = value
		case bool:
			out[p.Name] = strconv.FormatBool(value)
		case float64:
			if value == math.Trunc(value) && math.Abs(value) <= 1<<53-1 {
				out[p.Name] = strconv.FormatInt(int64(value), 10)
			}
		}
	}
	return out
}
