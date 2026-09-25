package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ResultDetails is what the UI is given of a tool result: the blocks the
// server sent, with the images and audio the model cannot see, bounded.
type ResultDetails struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	// Content is every block, in the server's order.
	Content []ContentDetail `json:"content"`
	// StructuredContent is the result's JSON, when the tool has an output
	// schema.
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	IsError           bool            `json:"is_error,omitempty"`
}

// ContentDetail is one block of a result or a prompt message, in Eika's
// wire form.
type ContentDetail struct {
	// Type is text, image, audio, resource_link, or resource.
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	URI         string `json:"uri,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Data is the base64 of an image, audio, or blob, empty when it was
	// over the budget.
	Data string `json:"data,omitempty"`
	// Size is the decoded size of Data, or of what was left out.
	Size int `json:"size,omitempty"`
	// Omitted reports media left out because the result carried more than
	// the budget.
	Omitted bool `json:"omitted,omitempty"`
}

// resultText is what the model reads of a tool result. A tool message is
// text, so an image or audio becomes a line saying the user can see it.
func resultText(r *CallToolResult) string {
	parts := make([]string, 0, len(r.Content)+1)
	hasText := false
	for _, c := range r.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
			hasText = true
		case "image", "audio":
			parts = append(parts, fmt.Sprintf("[%s %s, %s: shown to the user, not to you]", c.Type, c.MimeType, byteSize(decodedLen(c.Data))))
		case "resource_link":
			line := fmt.Sprintf("[resource %s: %s]", orName(c.Title, c.Name), c.URI)
			if c.Description != "" {
				line += " " + c.Description
			}
			parts = append(parts, line)
		case "resource":
			parts = append(parts, embeddedText(c.Resource))
			hasText = true
		default:
			parts = append(parts, fmt.Sprintf("[%s content]", c.Type))
		}
	}
	// A tool with an output schema should also send its JSON as text; one
	// that did not still has something to say.
	if !hasText && len(r.StructuredContent) > 0 {
		parts = append(parts, string(r.StructuredContent))
	}
	text := strings.Join(parts, "\n\n")
	if strings.TrimSpace(text) == "" {
		text = "(the tool returned nothing)"
	}
	return truncateMiddle(text, maxResultBytes)
}

// embeddedText renders an embedded resource: its text, or a line for a
// blob.
func embeddedText(r *ResourceContents) string {
	if r == nil {
		return "[empty resource]"
	}
	if r.Text != nil {
		return fmt.Sprintf("[resource %s]\n%s", r.URI, *r.Text)
	}
	size := 0
	if r.Blob != nil {
		size = decodedLen(*r.Blob)
	}
	return fmt.Sprintf("[binary resource %s, %s, %s]", r.URI, orName(r.MimeType, "unknown type"), byteSize(size))
}

// resultDetails renders a result for the UI.
func resultDetails(server, tool string, r *CallToolResult) ResultDetails {
	return ResultDetails{
		Server:            server,
		Tool:              tool,
		Content:           contentDetails(r.Content),
		StructuredContent: r.StructuredContent,
		IsError:           r.IsError,
	}
}

// contentDetails renders blocks for the UI, keeping media until the budget
// is spent.
func contentDetails(blocks []Content) []ContentDetail {
	out := make([]ContentDetail, 0, len(blocks))
	budget := maxMediaBytes
	keep := func(d *ContentDetail, data string) {
		d.Size = decodedLen(data)
		if len(data) <= budget {
			d.Data, budget = data, budget-len(data)
			return
		}
		d.Omitted = true
	}
	for _, c := range blocks {
		d := ContentDetail{Type: c.Type, MimeType: c.MimeType, URI: c.URI, Name: orName(c.Title, c.Name), Description: c.Description}
		switch c.Type {
		case "text":
			d.Text = truncateMiddle(c.Text, maxResultBytes)
		case "image", "audio":
			keep(&d, c.Data)
		case "resource":
			if r := c.Resource; r != nil {
				d.URI, d.MimeType = r.URI, r.MimeType
				if r.Text != nil {
					d.Text = truncateMiddle(*r.Text, maxResultBytes)
				} else if r.Blob != nil {
					keep(&d, *r.Blob)
				}
			}
		}
		out = append(out, d)
	}
	return out
}

// ResourceDetails renders what reading a resource returned, for the UI.
func ResourceDetails(contents []ResourceContents) []ContentDetail {
	blocks := make([]Content, 0, len(contents))
	for i := range contents {
		blocks = append(blocks, Content{Type: "resource", Resource: &contents[i]})
	}
	return contentDetails(blocks)
}

// RenderedPrompt is a prompt a server rendered, as the UI shows it.
type RenderedPrompt struct {
	Description string         `json:"description,omitempty"`
	Messages    []PromptDetail `json:"messages"`
}

// PromptDetail is one message of a rendered prompt.
type PromptDetail struct {
	// Role is user or assistant.
	Role    string        `json:"role"`
	Content ContentDetail `json:"content"`
}

// PromptDetails renders a prompt for the UI, its media within the budget a
// tool result has.
func PromptDetails(r *GetPromptResult) RenderedPrompt {
	blocks := make([]Content, 0, len(r.Messages))
	for _, m := range r.Messages {
		blocks = append(blocks, m.Content)
	}
	details := contentDetails(blocks)
	out := RenderedPrompt{Description: r.Description, Messages: make([]PromptDetail, 0, len(r.Messages))}
	for i, m := range r.Messages {
		out.Messages = append(out.Messages, PromptDetail{Role: m.Role, Content: details[i]})
	}
	return out
}

// decodedLen is the size of what a base64 string holds.
func decodedLen(b64 string) int {
	return base64.StdEncoding.DecodedLen(len(b64)) - strings.Count(b64[max(0, len(b64)-2):], "=")
}

// byteSize renders a size for a person.
func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func orName(title, name string) string {
	if title != "" {
		return title
	}
	return name
}

// truncateMiddle keeps the start and the end of a text longer than limit,
// with a note of what was cut, on character boundaries.
func truncateMiddle(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	head := limit * 2 / 3
	tail := limit - head
	for head > 0 && !isRuneStart(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !isRuneStart(s[start]) {
		start++
	}
	return fmt.Sprintf("%s\n\n... [%d bytes cut from the middle] ...\n\n%s", s[:head], start-head, s[start:])
}
