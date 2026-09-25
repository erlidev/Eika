package mcp

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func ptr[T any](v T) *T { return &v }

func TestResultText(t *testing.T) {
	png := base64.StdEncoding.EncodeToString(make([]byte, 2048))
	tests := []struct {
		name   string
		result CallToolResult
		want   []string
	}{
		{
			name:   "text and an image",
			result: CallToolResult{Content: []Content{{Type: "text", Text: "Here it is."}, {Type: "image", MimeType: "image/png", Data: png}}},
			want:   []string{"Here it is.", "[image image/png, 2.0 KB: shown to the user, not to you]"},
		},
		{
			name:   "a resource link",
			result: CallToolResult{Content: []Content{{Type: "resource_link", URI: "file:///a.txt", Name: "a.txt", Description: "The file."}}},
			want:   []string{"[resource a.txt: file:///a.txt] The file."},
		},
		{
			name:   "an embedded resource",
			result: CallToolResult{Content: []Content{{Type: "resource", Resource: &ResourceContents{URI: "file:///b", Text: ptr("body")}}}},
			want:   []string{"[resource file:///b]\nbody"},
		},
		{
			name:   "structured content alone",
			result: CallToolResult{StructuredContent: json.RawMessage(`{"temp":21}`)},
			want:   []string{`{"temp":21}`},
		},
		{
			name:   "nothing",
			result: CallToolResult{},
			want:   []string{"(the tool returned nothing)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resultText(&tt.result)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("resultText = %q, want it to contain %q", got, w)
				}
			}
		})
	}
}

func TestContentDetailsKeepMediaWithinBudget(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(make([]byte, maxMediaBytes/2))
	details := contentDetails([]Content{
		{Type: "image", MimeType: "image/png", Data: big},
		{Type: "text", Text: "between"},
		{Type: "audio", MimeType: "audio/wav", Data: big},
	})
	if details[0].Data == "" || details[0].Omitted {
		t.Errorf("first image = %+v, want it kept", details[0])
	}
	if details[2].Data != "" || !details[2].Omitted || details[2].Size != maxMediaBytes/2 {
		t.Errorf("audio = omitted %v, size %d; want it omitted with its size", details[2].Omitted, details[2].Size)
	}
	if details[1].Text != "between" {
		t.Errorf("text = %+v", details[1])
	}
}

func TestResourceDetails(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("abc"))
	details := ResourceDetails([]ResourceContents{
		{URI: "file:///a", MimeType: "text/plain", Text: ptr("hello")},
		{URI: "file:///b", MimeType: "application/octet-stream", Blob: &blob},
	})
	if details[0].Text != "hello" || details[0].URI != "file:///a" || details[0].MimeType != "text/plain" {
		t.Errorf("text resource = %+v", details[0])
	}
	if details[1].Data != blob || details[1].Size != 3 {
		t.Errorf("blob resource = %+v", details[1])
	}
}

func TestTruncateMiddleKeepsCharacters(t *testing.T) {
	s := strings.Repeat("é", 1000)
	got := truncateMiddle(s, 301)
	if !utf8.ValidString(got) {
		t.Error("truncateMiddle split a character")
	}
	if !strings.Contains(got, "bytes cut from the middle") {
		t.Errorf("truncateMiddle = %q, want a note of the cut", got)
	}
	if truncateMiddle("short", 10) != "short" {
		t.Error("truncateMiddle changed a short text")
	}
}

func TestDecodedLen(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 100} {
		b64 := base64.StdEncoding.EncodeToString(make([]byte, n))
		if got := decodedLen(b64); got != n {
			t.Errorf("decodedLen of %d bytes = %d", n, got)
		}
	}
}

func TestPromptDetails(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("png"))
	got := PromptDetails(&GetPromptResult{Description: "Review", Messages: []PromptMessage{
		{Role: "user", Content: Content{Type: "text", Text: "Look at this"}},
		{Role: "user", Content: Content{Type: "image", MimeType: "image/png", Data: png}},
	}})
	if got.Description != "Review" || len(got.Messages) != 2 {
		t.Fatalf("details = %+v", got)
	}
	if m := got.Messages[0]; m.Role != "user" || m.Content.Text != "Look at this" {
		t.Errorf("first message = %+v", m)
	}
	if m := got.Messages[1]; m.Content.Type != "image" || m.Content.Data != png || m.Content.Size != 3 {
		t.Errorf("image message = %+v", m)
	}
}
