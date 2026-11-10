package agent

import (
	"context"

	"github.com/erlidev/eika/internal/provider/counter"
)

// PartSize is the size of one part of a request, in tokens.
type PartSize struct {
	Tokens int `json:"tokens"`
	// Approximate is set when the number is not the endpoint's own tokenisation
	// of the part: it was estimated from bytes, a picture's cost was assumed, or
	// a tool was sized as JSON text instead of rendered by the template.
	Approximate bool `json:"approximate,omitempty"`
}

// SectionSize is the size of one section of the system prompt.
type SectionSize struct {
	Kind SectionKind `json:"kind"`
	PartSize
}

// ToolSize is the size of one tool definition.
type ToolSize struct {
	Name string `json:"name"`
	PartSize
}

// MessageSize is the size of one message as the request sends it.
type MessageSize struct {
	// Tokens is the message's text, its tool calls and its pictures.
	PartSize
	// Reasoning is the replayed reasoning, counted apart from the rest of the
	// message and absent when the message sends none.
	Reasoning int `json:"reasoning_tokens,omitempty"`
}

// Breakdown is where the tokens of one request go: each part counted on its
// own, in the order Context lists them. It does not say what the template and
// the formatting around the parts cost. That is the difference between the
// endpoint's count of the whole request and Parts, which a caller states as
// its own line and never spreads over the parts (docs/design/runtime.md 2).
type Breakdown struct {
	Sections []SectionSize `json:"sections"`
	Tools    []ToolSize    `json:"tools"`
	Messages []MessageSize `json:"messages"`
	// Parts is the sum of every part, reasoning included.
	Parts int `json:"parts"`
}

// Approximate reports whether any part of the breakdown is approximate.
func (b Breakdown) Approximate() bool {
	for _, s := range b.Sections {
		if s.Approximate {
			return true
		}
	}
	for _, t := range b.Tools {
		if t.Approximate {
			return true
		}
	}
	for _, m := range b.Messages {
		if m.Approximate {
			return true
		}
	}
	return false
}

// MeasureParts counts the parts of c with the session's counter, which
// tokenises each exactly where its endpoint can and says which are not.
// Counting is bounded by ctx: a part it has no time for is estimated, and the
// breakdown says so. Every part is remembered by the hash of its content, so a
// conversation that grew since the last call tokenises only what is new.
func MeasureParts(ctx context.Context, ctr *counter.Counter, c Context) Breakdown {
	b := Breakdown{
		Sections: make([]SectionSize, 0, len(c.Sections)),
		Tools:    make([]ToolSize, 0, len(c.Tools)),
		Messages: make([]MessageSize, 0, len(c.Messages)),
	}
	for _, s := range c.Sections {
		n, exact := ctr.Size(ctx, s.Text)
		b.Sections = append(b.Sections, SectionSize{Kind: s.Kind, PartSize: PartSize{Tokens: n, Approximate: !exact}})
		b.Parts += n
	}
	sizes, exact := ctr.ToolSizes(ctx, c.Request())
	for i, t := range c.Tools {
		n := 0
		if i < len(sizes) {
			n = sizes[i]
		}
		b.Tools = append(b.Tools, ToolSize{Name: t.Name, PartSize: PartSize{Tokens: n, Approximate: !exact}})
		b.Parts += n
	}
	for _, m := range c.Messages {
		approximate := false
		text := func(s string) int {
			n, exact := ctr.Size(ctx, s)
			approximate = approximate || !exact
			return n
		}
		size := MessageSize{}
		size.Tokens = text(m.ModelText())
		for _, call := range m.ToolCalls {
			size.Tokens += text(call.Name) + text(string(call.Arguments))
		}
		for _, img := range m.Images {
			n, measured := ctr.ImageSize(img)
			size.Tokens += n
			approximate = approximate || !measured
		}
		size.Reasoning = text(m.Reasoning)
		size.Approximate = approximate
		b.Messages = append(b.Messages, size)
		b.Parts += size.Tokens + size.Reasoning
	}
	return b
}
