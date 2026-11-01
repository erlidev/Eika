package bench

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Flags is the argument list of a server container, as `docker inspect`
// reports it. The values that are secrets are kept out of every report with
// Redact; nothing here reads the environment, where llama.cpp's own key
// lives.
type Flags []string

// Edit is one change to a flag list. A variant is the recorded flags with its
// edits applied, and the report states them.
type Edit struct {
	// Op is "add" (a flag with no value), "set" (a flag with a value,
	// replaced where it is present) or "drop".
	Op string
	// Flag is the flag as written, such as --no-kv-unified.
	Flag string
	// Value is the value of a "set"; for a "drop" it says whether the flag
	// takes a value, which goes with it.
	Value string
}

// AddFlag adds a flag that takes no value.
func AddFlag(flag string) Edit { return Edit{Op: "add", Flag: flag} }

// SetFlag sets a flag that takes a value.
func SetFlag(flag, value string) Edit { return Edit{Op: "set", Flag: flag, Value: value} }

// DropFlag removes a flag that takes no value.
func DropFlag(flag string) Edit { return Edit{Op: "drop", Flag: flag} }

// DropFlagWithValue removes a flag and the value after it.
func DropFlagWithValue(flag string) Edit { return Edit{Op: "drop", Flag: flag, Value: "value"} }

// String writes the edit as it reads on a command line.
func (e Edit) String() string {
	switch e.Op {
	case "set":
		return e.Flag + " " + e.Value
	case "drop":
		return "without " + e.Flag
	}
	return e.Flag
}

// Apply returns f with the edits applied in order. f is not changed. It
// fails on an edit that cannot be applied, such as setting a flag twice
// named, so a variant cannot silently be the baseline.
func (f Flags) Apply(edits ...Edit) (Flags, error) {
	out := slices.Clone(f)
	for _, e := range edits {
		i := slices.Index(out, e.Flag)
		switch e.Op {
		case "add":
			if i < 0 {
				out = append(out, e.Flag)
			}
		case "set":
			switch {
			case i < 0:
				out = append(out, e.Flag, e.Value)
			case i+1 >= len(out):
				return nil, fmt.Errorf("set %s: the flag has no value to replace", e.Flag)
			default:
				out[i+1] = e.Value
			}
		case "drop":
			if i < 0 {
				return nil, fmt.Errorf("drop %s: the flag is not among the recorded flags", e.Flag)
			}
			n := 1
			if e.Value != "" {
				n = 2
			}
			out = slices.Delete(out, i, min(i+n, len(out)))
		default:
			return nil, fmt.Errorf("edit %q has no operation", e.Flag)
		}
	}
	return out, nil
}

// Value returns the value after flag, and whether the flag is there with one.
func (f Flags) Value(flag string) (string, bool) {
	i := slices.Index(f, flag)
	if i < 0 || i+1 >= len(f) {
		return "", false
	}
	return f[i+1], true
}

// Has reports whether flag is present.
func (f Flags) Has(flag string) bool { return slices.Contains(f, flag) }

// secretFlag matches a flag whose value is a credential.
var secretFlag = regexp.MustCompile(`(?i)(key|token|secret|password)`)

// Redact returns f with the value of any flag that names a credential
// replaced, so a command line can be logged and reported. It is applied to
// everything read from a container that is not the chat model's own.
func (f Flags) Redact() Flags {
	out := slices.Clone(f)
	for i, a := range out {
		if !strings.HasPrefix(a, "-") || !secretFlag.MatchString(a) {
			continue
		}
		if name, _, ok := strings.Cut(a, "="); ok {
			out[i] = name + "=REDACTED"
		} else if i+1 < len(out) {
			out[i+1] = "REDACTED"
		}
	}
	return out
}

// String joins the flags for a log line.
func (f Flags) String() string { return strings.Join(f, " ") }
