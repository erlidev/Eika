package servertest_test

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/server/servertest"
)

type inner struct {
	Note string `json:"note"`
}

type sample struct {
	inner
	Name    string            `json:"name"`
	Count   int               `json:"count,omitempty"`
	When    time.Time         `json:"when,omitempty"`
	Parent  *inner            `json:"parent"`
	Tags    []string          `json:"tags,omitempty"`
	Labels  map[string]string `json:"labels"`
	Raw     json.RawMessage   `json:"raw,omitempty"`
	Addr    netip.Addr        `json:"addr"`
	Quoted  int               `json:"quoted,string"`
	Skipped string            `json:"-"`
	hidden  string
}

// The unexported field is there to be left out, not read.
var _ = sample{}.hidden

type custom struct{}

func (custom) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

type node struct {
	Name     string `json:"name"`
	Children []node `json:"children"`
}

func shapeOf(t *testing.T, v any, opts servertest.Options) *servertest.Shape {
	t.Helper()
	s, err := servertest.Of(reflect.TypeOf(v), opts)
	if err != nil {
		t.Fatalf("Of(%T): %v", v, err)
	}
	return s
}

func TestOfReadsTheFieldsEncodingJSONWrites(t *testing.T) {
	data, err := json.Marshal(shapeOf(t, sample{}, servertest.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"object":{` +
		`"addr":"string",` +
		`"count":"number",` +
		`"labels":{"nullable":{"map":"string"}},` +
		`"name":"string",` +
		`"note":"string",` +
		`"parent":{"nullable":{"object":{"note":"string"}}},` +
		`"quoted":"string",` +
		`"raw":"any",` +
		`"tags":{"array":"string"},` +
		`"when":"string"` +
		`},"optional":["count","raw","tags"]}`
	if string(data) != want {
		t.Errorf("shape =\n%s\nwant\n%s", data, want)
	}
}

func TestOfReadsARequestAsTheDecoderAcceptsIt(t *testing.T) {
	data, err := json.Marshal(shapeOf(t, sample{}, servertest.Options{Request: true}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "nullable") || strings.Contains(string(data), "optional") {
		t.Errorf("request shape %s marks fields optional or nullable; Check accepts both in a request", data)
	}
}

func TestOfRefusesACustomEncodingWithoutAStandIn(t *testing.T) {
	type wrapper struct {
		C custom `json:"c"`
	}
	if _, err := servertest.Of(reflect.TypeFor[wrapper](), servertest.Options{}); err == nil {
		t.Fatal("Of read a type that encodes itself")
	}
	s := shapeOf(t, wrapper{}, servertest.Options{StandIns: map[reflect.Type]reflect.Type{
		reflect.TypeFor[custom](): reflect.TypeFor[string](),
	}})
	if got := s.Fields["c"].Kind; got != servertest.String {
		t.Errorf("stood-in field kind = %s, want string", got)
	}
}

func TestReaderRefersToARecursiveType(t *testing.T) {
	if _, err := servertest.Of(reflect.TypeFor[node](), servertest.Options{}); err == nil {
		t.Error("Of read a recursive type it cannot write out")
	}
	var r servertest.Reader
	s, err := r.Of(reflect.TypeFor[node]())
	if err != nil {
		t.Fatalf("Reader.Of: %v", err)
	}
	if _, ok := r.Types["servertest_test.node"]; !ok {
		t.Fatalf("Types = %v, want node", r.Types)
	}
	good := map[string]any{"name": "a", "children": []any{map[string]any{"name": "b", "children": nil}}}
	if p := s.Check(good, false, r.Types); len(p) > 0 {
		t.Errorf("a valid tree: %v", p)
	}
	bad := map[string]any{"name": "a", "children": []any{map[string]any{"name": 1.0, "children": nil}}}
	if p := s.Check(bad, false, r.Types); !slices.Equal(p, []string{"$.children[0].name: number, want string"}) {
		t.Errorf("a child with a wrong name: %v", p)
	}
}

func TestCheck(t *testing.T) {
	response := shapeOf(t, sample{}, servertest.Options{})
	request := shapeOf(t, sample{}, servertest.Options{Request: true})
	complete := func() map[string]any {
		return map[string]any{
			"addr": "::1", "labels": nil, "name": "a", "note": "", "parent": nil,
			"quoted": "1", "when": "2026-01-01T00:00:00Z",
		}
	}
	tests := []struct {
		name    string
		shape   *servertest.Shape
		request bool
		edit    func(map[string]any)
		want    []string
	}{
		{"a complete response", response, false, func(map[string]any) {}, nil},
		{"optional fields present", response, false, func(v map[string]any) {
			v["count"], v["tags"], v["raw"] = 3.0, []any{"x"}, map[string]any{"any": true}
		}, nil},
		{"an unknown field", response, false, func(v map[string]any) { v["colour"] = "red" },
			[]string{"$.colour: unknown field"}},
		{"a missing field", response, false, func(v map[string]any) { delete(v, "name") },
			[]string{"$.name: missing"}},
		{"a wrong type", response, false, func(v map[string]any) { v["name"] = true },
			[]string{"$.name: boolean, want string"}},
		{"null where none is allowed", response, false, func(v map[string]any) { v["name"] = nil },
			[]string{"$.name: null, want string"}},
		{"null inside an array", response, false, func(v map[string]any) { v["tags"] = []any{nil} },
			[]string{"$.tags[0]: null, want string"}},
		{"a wrong map value", response, false, func(v map[string]any) { v["labels"] = map[string]any{"k": 1.0} },
			[]string{"$.labels.k: number, want string"}},
		{"a request may leave fields out", request, true, func(v map[string]any) {
			delete(v, "name")
			v["count"] = nil
		}, nil},
		{"a request may not add one", request, true, func(v map[string]any) { v["colour"] = "red" },
			[]string{"$.colour: unknown field"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := complete()
			tt.edit(v)
			if got := tt.shape.Check(v, tt.request, nil); !slices.Equal(got, tt.want) {
				t.Errorf("Check = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShapesRoundTripThroughJSON(t *testing.T) {
	var r servertest.Reader
	s, err := r.Of(reflect.TypeFor[struct {
		S    sample `json:"s"`
		Tree node   `json:"tree"`
	}]())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back servertest.Shape
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	again, err := json.Marshal(&back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Errorf("round trip:\n%s\nbecame\n%s", data, again)
	}
}

func TestContractMatchPrefersALiteralSegment(t *testing.T) {
	c := servertest.Contract{Routes: map[string]servertest.Route{
		"POST /api/models/{id}": {},
		"POST /api/models/test": {},
		"GET /api/models/{id}":  {},
	}}
	for path, want := range map[string]string{
		"/api/models/test": "POST /api/models/test",
		"/api/models/m1":   "POST /api/models/{id}",
	} {
		if got, _, ok := c.Match("POST", path); !ok || got != want {
			t.Errorf("Match(POST %s) = %q, %v; want %q", path, got, ok, want)
		}
	}
	if _, _, ok := c.Match("POST", "/api/models"); ok {
		t.Error("Match found a route for a path with fewer segments")
	}
}
