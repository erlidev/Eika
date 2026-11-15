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
// tag, one "name: value" line per field, the closing tag. A value of several
// lines, such as a notice, indents the lines after its first by two spaces, so
// that every field still starts a line of its own. A block with no fields is
// empty text.
func contextBlock(fields ...contextField) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<context>\n")
	for _, f := range fields {
		lines := strings.Split(f.value, "\n")
		b.WriteString(f.name + ": " + lines[0] + "\n")
		for _, line := range lines[1:] {
			if line != "" {
				b.WriteString("  " + line)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("</context>")
	return b.String()
}

// Notice is harness text for Eika's next user-side message, such as a
// subagent's report: ID is the notice entry that records it, and Text is what
// the <context> block says.
type Notice struct {
	ID   string
	Text string
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
	a.locationMu.RLock()
	location := a.location
	a.locationMu.RUnlock()
	fields := []contextField{timeField(a.opts.Clock(), location)}
	if a.opts.Channel != "" {
		fields = append(fields, contextField{name: "channel", value: a.opts.Channel})
	}
	if a.opts.Notices != nil {
		for _, n := range a.opts.Notices() {
			fields = append(fields, contextField{name: "notice", value: n.Text})
			m.Notices = append(m.Notices, n.ID)
		}
	}
	m.Context = contextBlock(fields...)
	return m
}

// PreambleLocation returns the zone stored in the snapshot, or UTC.
func PreambleLocation(text string) *time.Location {
	text = "\n\n" + text
	start := strings.LastIndex(text, "\n\nTime zone\n")
	if start < 0 {
		return time.UTC
	}
	zone := strings.SplitN(text[start+len("\n\nTime zone\n"):], "\n", 2)[0]
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
}
