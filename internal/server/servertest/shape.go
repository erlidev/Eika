package servertest

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// Kind is what a Shape describes.
type Kind string

// The kinds of Shape. The first four are written in a contract as the bare
// string; the others as an object keyed by the kind.
const (
	String  Kind = "string"
	Number  Kind = "number"
	Boolean Kind = "boolean"
	// Any is a value whose type fixes no shape, such as a json.RawMessage or
	// an interface.
	Any    Kind = "any"
	Array  Kind = "array"
	Map    Kind = "map"
	Object Kind = "object"
	// Nullable is a value that is either null or Elem.
	Nullable Kind = "nullable"
	// Ref is a value of the recursive type Name, whose shape is in the
	// contract's Types.
	Ref Kind = "ref"
)

// Shape is the JSON form of a value.
type Shape struct {
	Kind Kind
	// Elem is an Array's element, a Map's value, and a Nullable's value when
	// it is not null.
	Elem *Shape
	// Fields are an Object's fields by JSON name.
	Fields map[string]*Shape
	// Optional names the fields of an Object that may be left out, sorted.
	Optional []string
	// Name is the type a Ref stands for.
	Name string
}

// MarshalJSON writes a primitive as its kind, `"string"`, and anything else
// as an object keyed by its kind: `{"array": ...}`, `{"object": {...},
// "optional": [...]}`.
func (s *Shape) MarshalJSON() ([]byte, error) {
	switch s.Kind {
	case String, Number, Boolean, Any:
		return json.Marshal(string(s.Kind))
	case Array, Map, Nullable:
		return json.Marshal(map[string]*Shape{string(s.Kind): s.Elem})
	case Ref:
		return json.Marshal(map[string]string{string(Ref): s.Name})
	case Object:
		out := map[string]any{"object": s.Fields}
		if len(s.Optional) > 0 {
			out["optional"] = s.Optional
		}
		return json.Marshal(out)
	}
	return nil, fmt.Errorf("shape of unknown kind %q", s.Kind)
}

// UnmarshalJSON reads the form MarshalJSON writes.
func (s *Shape) UnmarshalJSON(data []byte) error {
	var kind string
	if json.Unmarshal(data, &kind) == nil {
		switch Kind(kind) {
		case String, Number, Boolean, Any:
			*s = Shape{Kind: Kind(kind)}
			return nil
		}
		return fmt.Errorf("unknown shape %q", kind)
	}
	var wire struct {
		Array    *Shape            `json:"array"`
		Map      *Shape            `json:"map"`
		Nullable *Shape            `json:"nullable"`
		Ref      string            `json:"ref"`
		Object   map[string]*Shape `json:"object"`
		Optional []string          `json:"optional"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	switch {
	case wire.Array != nil:
		*s = Shape{Kind: Array, Elem: wire.Array}
	case wire.Map != nil:
		*s = Shape{Kind: Map, Elem: wire.Map}
	case wire.Nullable != nil:
		*s = Shape{Kind: Nullable, Elem: wire.Nullable}
	case wire.Ref != "":
		*s = Shape{Kind: Ref, Name: wire.Ref}
	case wire.Object != nil:
		*s = Shape{Kind: Object, Fields: wire.Object, Optional: wire.Optional}
	default:
		return fmt.Errorf("unknown shape %s", data)
	}
	return nil
}

// String names the shape for a message: `string`, `object or null`.
func (s *Shape) String() string {
	switch s.Kind {
	case Nullable:
		return s.Elem.String() + " or null"
	case Ref:
		return "object"
	}
	return string(s.Kind)
}

// Options tune how Of reads a type.
type Options struct {
	// Request reads the type as a request body. The API's decoder accepts a
	// field that is missing or null, so a request shape marks nothing
	// optional or nullable, and Check with request set accepts both.
	Request bool
	// StandIns give, for a type with a MarshalJSON or UnmarshalJSON of its
	// own, a type that has the same wire form. Reflection cannot see through
	// a custom encoding, so Of refuses such a type without one.
	StandIns map[reflect.Type]reflect.Type
}

var (
	timeType        = reflect.TypeFor[time.Time]()
	rawType         = reflect.TypeFor[json.RawMessage]()
	marshalerType   = reflect.TypeFor[json.Marshaler]()
	unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textType        = reflect.TypeFor[encoding.TextMarshaler]()
)

// Reader reads the shapes of types. A struct that contains itself, such as
// a tree's node, is a Ref inside itself, and its shape is kept in Types.
type Reader struct {
	Options
	// Types are the shapes of the recursive types read so far, by name.
	Types map[string]*Shape
	// seen holds the structs being read, outermost first.
	seen []reflect.Type
	// recursive marks the structs found inside themselves.
	recursive map[reflect.Type]bool
}

// Of returns the shape encoding/json gives values of t.
func (r *Reader) Of(t reflect.Type) (*Shape, error) {
	return r.shape(t)
}

// Of returns the shape encoding/json gives values of t, which must not
// contain itself; a Reader reads one that does.
func Of(t reflect.Type, opts Options) (*Shape, error) {
	r := Reader{Options: opts}
	s, err := r.Of(t)
	if err == nil && len(r.Types) > 0 {
		return nil, fmt.Errorf("%s contains itself: read it with a Reader", t)
	}
	return s, err
}

func (r *Reader) shape(t reflect.Type) (*Shape, error) {
	switch t {
	case timeType:
		return &Shape{Kind: String}, nil
	case rawType:
		return &Shape{Kind: Any}, nil
	}
	if standIn, ok := r.StandIns[t]; ok {
		return r.shape(standIn)
	}
	if t.Kind() == reflect.Pointer {
		// Before the checks for a custom encoding, which a pointer shares
		// with what it points to.
		elem, err := r.shape(t.Elem())
		if err != nil {
			return nil, err
		}
		return r.nullable(elem), nil
	}
	if t.Kind() != reflect.Interface && implements(t, marshalerType, unmarshalerType) {
		return nil, fmt.Errorf("%s encodes itself: add a stand-in with its wire form", t)
	}
	if t.Kind() != reflect.Interface && implements(t, textType) {
		return &Shape{Kind: String}, nil
	}
	switch t.Kind() {
	case reflect.String:
		return &Shape{Kind: String}, nil
	case reflect.Bool:
		return &Shape{Kind: Boolean}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return &Shape{Kind: Number}, nil
	case reflect.Interface:
		return &Shape{Kind: Any}, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			// A byte slice is written as base64 text.
			return r.nullable(&Shape{Kind: String}), nil
		}
		elem, err := r.shape(t.Elem())
		if err != nil {
			return nil, err
		}
		return r.nullable(&Shape{Kind: Array, Elem: elem}), nil
	case reflect.Array:
		elem, err := r.shape(t.Elem())
		if err != nil {
			return nil, err
		}
		return &Shape{Kind: Array, Elem: elem}, nil
	case reflect.Map:
		elem, err := r.shape(t.Elem())
		if err != nil {
			return nil, err
		}
		return r.nullable(&Shape{Kind: Map, Elem: elem}), nil
	case reflect.Struct:
		return r.object(t)
	}
	return nil, fmt.Errorf("%s has no JSON form", t)
}

// nullable marks what a nil pointer, slice, or map makes null. A request
// needs no mark, since Check accepts null anywhere in one.
func (r *Reader) nullable(s *Shape) *Shape {
	if r.Request || s.Kind == Nullable || s.Kind == Any {
		return s
	}
	return &Shape{Kind: Nullable, Elem: s}
}

func (r *Reader) object(t reflect.Type) (*Shape, error) {
	if slices.Contains(r.seen, t) {
		if r.recursive == nil {
			r.recursive = map[reflect.Type]bool{}
		}
		r.recursive[t] = true
		return &Shape{Kind: Ref, Name: t.String()}, nil
	}
	r.seen = append(r.seen, t)
	defer func() { r.seen = r.seen[:len(r.seen)-1] }()
	s := &Shape{Kind: Object, Fields: map[string]*Shape{}}
	if err := r.fields(t, s); err != nil {
		return nil, err
	}
	sort.Strings(s.Optional)
	if r.recursive[t] {
		if r.Types == nil {
			r.Types = map[string]*Shape{}
		}
		r.Types[t.String()] = s
	}
	return s, nil
}

// fields adds t's fields to s as encoding/json writes them: an untagged
// embedded struct's fields are the outer struct's own, and a field tagged
// omitempty or omitzero may be left out.
func (r *Reader) fields(t reflect.Type, s *Shape) error {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, flags, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			inner := f.Type
			if inner.Kind() == reflect.Pointer {
				inner = inner.Elem()
			}
			if inner.Kind() == reflect.Struct && !implements(inner, marshalerType, textType) {
				if err := r.fields(inner, s); err != nil {
					return err
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		shape, err := r.shape(f.Type)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t, f.Name, err)
		}
		options := strings.Split(flags, ",")
		if slices.Contains(options, "string") {
			shape = &Shape{Kind: String}
		}
		if !r.Request && omitted(f.Type, options) {
			// Only an empty value is left out, and a nil pointer, slice, or
			// map is empty, so one that is written is never null.
			if shape.Kind == Nullable {
				shape = shape.Elem
			}
			s.Optional = append(s.Optional, name)
		}
		s.Fields[name] = shape
	}
	return nil
}

// omitted reports whether encoding/json may leave out a field of type t with
// these tag options. omitempty never leaves out a struct.
func omitted(t reflect.Type, options []string) bool {
	if slices.Contains(options, "omitzero") {
		return true
	}
	return slices.Contains(options, "omitempty") && t.Kind() != reflect.Struct
}

// implements reports whether t or a pointer to it implements any of ifaces.
func implements(t reflect.Type, ifaces ...reflect.Type) bool {
	for _, iface := range ifaces {
		if t.Implements(iface) || reflect.PointerTo(t).Implements(iface) {
			return true
		}
	}
	return false
}

// Check reports every way v, a value decoded from JSON into an any, differs
// from s, one problem per line, each starting with the path to the value:
// `$.projects[0].name: number, want string`. In a request a field may be
// missing or null, as the API's decoder allows, but an unknown field is still
// a problem, as the decoder refuses it.
// types resolves the Refs in s.
func (s *Shape) Check(v any, request bool, types map[string]*Shape) []string {
	c := checker{request: request, types: types}
	c.check(s, v, "$")
	return c.problems
}

type checker struct {
	request  bool
	types    map[string]*Shape
	problems []string
}

func (c *checker) check(s *Shape, v any, path string) {
	fail := func(format string, args ...any) {
		c.problems = append(c.problems, path+": "+fmt.Sprintf(format, args...))
	}
	if s.Kind == Ref {
		def, ok := c.types[s.Name]
		if !ok {
			fail("no shape for %s", s.Name)
			return
		}
		s = def
	}
	if v == nil {
		if s.Kind != Nullable && s.Kind != Any && !c.request {
			fail("null, want %s", s)
		}
		return
	}
	switch s.Kind {
	case Any:
	case Nullable:
		c.check(s.Elem, v, path)
	case String, Number, Boolean:
		if got := kindOf(v); got != s.Kind {
			fail("%s, want %s", got, s.Kind)
		}
	case Array:
		items, ok := v.([]any)
		if !ok {
			fail("%s, want array", kindOf(v))
			return
		}
		for i, item := range items {
			c.check(s.Elem, item, fmt.Sprintf("%s[%d]", path, i))
		}
	case Map:
		m, ok := v.(map[string]any)
		if !ok {
			fail("%s, want map", kindOf(v))
			return
		}
		for _, k := range sortedKeys(m) {
			c.check(s.Elem, m[k], path+"."+k)
		}
	case Object:
		m, ok := v.(map[string]any)
		if !ok {
			fail("%s, want object", kindOf(v))
			return
		}
		for _, k := range sortedKeys(m) {
			field, known := s.Fields[k]
			if !known {
				c.problems = append(c.problems, fmt.Sprintf("%s.%s: unknown field", path, k))
				continue
			}
			c.check(field, m[k], path+"."+k)
		}
		if c.request {
			return
		}
		for _, k := range sortedKeys(s.Fields) {
			if _, present := m[k]; !present && !slices.Contains(s.Optional, k) {
				c.problems = append(c.problems, fmt.Sprintf("%s.%s: missing", path, k))
			}
		}
	}
}

// kindOf names the kind of a value decoded into an any.
func kindOf(v any) Kind {
	switch v.(type) {
	case string:
		return String
	case float64, json.Number:
		return Number
	case bool:
		return Boolean
	case []any:
		return Array
	case map[string]any:
		return Object
	}
	return Any
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
