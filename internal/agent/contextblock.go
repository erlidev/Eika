package agent

import (
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// contextField is one line of a <context> block: a name and its value. The
// block is a list of them in a fixed order, so a channel or a notice joins it
// as one more line and nothing about its shape changes.
type contextField struct {
	name, value string
}

// contextBlock renders the <context> block a user-side message ends with: the
// tag, one "name: value" line per field, the closing tag. A block with no
// fields is empty text.
func contextBlock(fields ...contextField) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<context>\n")
	for _, f := range fields {
		b.WriteString(f.name + ": " + f.value + "\n")
	}
	b.WriteString("</context>")
	return b.String()
}

// timeLayout writes a time to the minute with its weekday, its offset from UTC
// and the name of its zone: "Sat 2026-10-31 14:05 +00:00 (UTC)". The offset
// is numeric because a zone's abbreviation is ambiguous, and the name beside it
// says which zone it is.
const timeLayout = "Mon 2006-01-02 15:04 -07:00"

// timeField is the "time" line of the block for the instant t in loc.
func timeField(t time.Time, loc *time.Location) contextField {
	t = t.In(loc)
	return contextField{name: "time", value: t.Format(timeLayout) + " (" + loc.String() + ")"}
}

// stamp returns a user-side message with its context block written, if it does
// not have one. The block is written when the message is first sent and is
// never written again: a message a failed turn gave back to its queue keeps the
// time it was sent at, and one read back from the store keeps the very bytes it
// had, so a resumed session sends the same prefix. Nothing is stamped that is
// not a user's message (a tool result is not one, and the summary a compaction
// leaves stands for earlier messages).
func (a *Agent) stamp(m provider.Message) provider.Message {
	if m.Role != provider.RoleUser || m.Summary || m.Context != "" {
		return m
	}
	fields := []contextField{timeField(a.opts.Clock(), a.opts.Location)}
	if a.opts.Channel != "" {
		fields = append(fields, contextField{name: "channel", value: a.opts.Channel})
	}
	m.Context = contextBlock(fields...)
	return m
}
