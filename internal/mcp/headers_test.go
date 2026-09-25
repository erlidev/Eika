package mcp

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

func TestHeaderValue(t *testing.T) {
	// The encoding table of the 2026-07-28 revision.
	tests := []struct{ in, want string }{
		{"us-west1", "us-west1"},
		{"Hello, 世界", "=?base64?SGVsbG8sIOS4lueVjA==?="},
		{" padded ", "=?base64?IHBhZGRlZCA=?="},
		{"line1\nline2", "=?base64?bGluZTEKbGluZTI=?="},
		{"tab\tinside", "tab\tinside"},
		{"=?base64?abc?=", "=?base64?PT9iYXNlNjQ/YWJjPz0=?="},
		{"", ""},
	}
	for _, tt := range tests {
		if got := headerValue(tt.in); got != tt.want {
			t.Errorf("headerValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHeaderParams(t *testing.T) {
	tests := []struct {
		name, schema string
		want         map[string]string // header name to dotted path
		err          string
	}{
		{
			name:   "none",
			schema: `{"type":"object","properties":{"q":{"type":"string"}}}`,
			want:   map[string]string{},
		},
		{
			name:   "top level and nested",
			schema: `{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"},"opts":{"type":"object","properties":{"dry":{"type":"boolean","x-mcp-header":"Dry-Run"}}}}}`,
			want:   map[string]string{"Region": "region", "Dry-Run": "opts.dry"},
		},
		{
			name:   "not a header token",
			schema: `{"type":"object","properties":{"a":{"type":"string","x-mcp-header":"Has Space"}}}`,
			err:    "not a valid HTTP header name",
		},
		{
			name:   "used twice ignoring case",
			schema: `{"type":"object","properties":{"a":{"type":"string","x-mcp-header":"Region"},"b":{"type":"string","x-mcp-header":"region"}}}`,
			err:    "used twice",
		},
		{
			name:   "an object parameter",
			schema: `{"type":"object","properties":{"a":{"type":"object","x-mcp-header":"A"}}}`,
			err:    "not a string, integer, or boolean",
		},
		{
			name:   "a number parameter",
			schema: `{"type":"object","properties":{"a":{"type":"number","x-mcp-header":"A"}}}`,
			err:    "not a string, integer, or boolean",
		},
		{
			name:   "inside an array",
			schema: `{"type":"object","properties":{"list":{"type":"array","items":{"type":"string","x-mcp-header":"Item"}}}}`,
			err:    "not reachable",
		},
		{
			name:   "inside anyOf",
			schema: `{"type":"object","anyOf":[{"properties":{"a":{"type":"string","x-mcp-header":"A"}}}]}`,
			err:    "not reachable",
		},
		{
			name:   "in definitions",
			schema: `{"type":"object","$defs":{"thing":{"type":"string","x-mcp-header":"Thing"}}}`,
			err:    "not reachable",
		},
		{
			name:   "not a string",
			schema: `{"type":"object","properties":{"a":{"type":"string","x-mcp-header":3}}}`,
			err:    "is not a string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := headerParams(json.RawMessage(tt.schema))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("headerParams = %v, %v; want an error with %q", params, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("headerParams: %v", err)
			}
			got := map[string]string{}
			for _, p := range params {
				got[p.Name] = strings.Join(p.Path, ".")
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("params = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParamHeaders(t *testing.T) {
	params := []headerParam{
		{Name: "Region", Path: []string{"region"}},
		{Name: "Days", Path: []string{"days"}},
		{Name: "Dry", Path: []string{"opts", "dry"}},
		{Name: "Absent", Path: []string{"absent"}},
		{Name: "Null", Path: []string{"null"}},
		{Name: "Fraction", Path: []string{"fraction"}},
	}
	got := paramHeaders(params, json.RawMessage(`{"region":"eu","days":14,"opts":{"dry":false},"null":null,"fraction":1.5}`))
	want := map[string]string{"Region": "eu", "Days": "14", "Dry": "false"}
	if !maps.Equal(got, want) {
		t.Errorf("paramHeaders = %v, want %v", got, want)
	}
	if got := paramHeaders(params, json.RawMessage(`not json`)); got != nil {
		t.Errorf("paramHeaders of broken arguments = %v, want nil", got)
	}
}
