package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
)

// Count is a number of tokens that a server may not have reported. The zero
// value is "not reported", which is a different statement from a reported
// zero: a server that says it reused no cached tokens has said something, and
// one that has no such field has not. A Count is comparable, and encodes as a
// JSON number, or null when not reported.
//
// Arithmetic on Counts never treats a missing value as zero: a sum with a term
// that was not reported is not reported either, so a total can only be read as
// "the sum of what every call said".
type Count struct {
	n  int
	ok bool
}

// Reported returns a Count the server stated as n.
func Reported(n int) Count { return Count{n: n, ok: true} }

// Value returns the count and whether the server reported it.
func (c Count) Value() (int, bool) { return c.n, c.ok }

// Reported reports whether the server stated this count.
func (c Count) Reported() bool { return c.ok }

// Or returns the count, or unreported when the server did not state it. It is
// for a caller that has a stated fallback of its own, such as an estimate; it
// is not a way to read "not reported" as zero.
func (c Count) Or(unreported int) int {
	if c.ok {
		return c.n
	}
	return unreported
}

// Plus returns the sum of two counts, not reported unless both were.
func (c Count) Plus(d Count) Count {
	if !c.ok || !d.ok {
		return Count{}
	}
	return Reported(c.n + d.n)
}

// Ptr returns the count as a pointer, nil when it was not reported, which is
// how the store and the wire types to the UI carry it.
func (c Count) Ptr() *int {
	if !c.ok {
		return nil
	}
	n := c.n
	return &n
}

// MarshalJSON writes the count as a number, or null when not reported.
func (c Count) MarshalJSON() ([]byte, error) {
	if !c.ok {
		return []byte("null"), nil
	}
	return json.Marshal(c.n)
}

// UnmarshalJSON reads a whole non-negative number as a reported count and
// null as not reported.
func (c *Count) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*c = Count{}
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("decode token count: %w", err)
	}
	if f < 0 || f != math.Trunc(f) || f > math.MaxInt32 {
		return errors.New("decode token count: not a whole non-negative number")
	}
	*c = Reported(int(f))
	return nil
}

// LogValue logs the count as a number, and as nil when not reported, so a log
// line never shows a count the server did not state as 0.
func (c Count) LogValue() slog.Value {
	if !c.ok {
		return slog.AnyValue(nil)
	}
	return slog.IntValue(c.n)
}
