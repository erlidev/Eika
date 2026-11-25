package server

import "github.com/erlidev/eika/internal/memory"

// memoryItem is one memory in a list or search result, without its body.
// Snippet is set by a search; Updated and Expires by a list.
type memoryItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	State       string `json:"state"`
	Pinned      bool   `json:"pinned"`
	Expires     string `json:"expires,omitempty"`
	Updated     string `json:"updated,omitempty"`
	ContentSHA  string `json:"content_sha,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
}

// memoriesResponse is a page of memories. NextCursor is set when more follow.
type memoriesResponse struct {
	Memories   []memoryItem `json:"memories"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// memoryBody is one memory in full. ContentSHA is the precondition of every
// change to it. Screen is the verdict behind a pending memory, and
// ArchiveReason is why an archived one was archived.
type memoryBody struct {
	Name          string               `json:"name"`
	Description   string               `json:"description"`
	Type          string               `json:"type"`
	Source        string               `json:"source"`
	Created       string               `json:"created"`
	Updated       string               `json:"updated"`
	Expires       string               `json:"expires"`
	Pinned        bool                 `json:"pinned"`
	State         string               `json:"state"`
	Body          string               `json:"body"`
	ContentSHA    string               `json:"content_sha"`
	Screen        *memory.ScreenResult `json:"screen,omitempty"`
	ArchiveReason string               `json:"archive_reason,omitempty"`
}

// editMemoryRequest changes a memory's text, type or expiry. ContentSHA is
// required. A field left out is kept; Expires "" clears the expiry.
type editMemoryRequest struct {
	ContentSHA  *string `json:"content_sha"`
	Description *string `json:"description"`
	Type        *string `json:"type"`
	Body        *string `json:"body"`
	Expires     *string `json:"expires"`
}

// memoryPreconditionRequest carries an optional content_sha for pin, unpin.
type memoryPreconditionRequest struct {
	ContentSHA *string `json:"content_sha"`
}

// archiveMemoryRequest archives a memory. Reason defaults to "archived by the
// user".
type archiveMemoryRequest struct {
	ContentSHA *string `json:"content_sha"`
	Reason     *string `json:"reason"`
}

// inboxResponse is a page of pending memories in full, with the screen verdict
// the Inbox shows. Pending is the total number waiting, not the page's size.
type inboxResponse struct {
	Pending    int          `json:"pending"`
	Memories   []memoryBody `json:"memories"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// inboxCountResponse is the number of pending memories.
type inboxCountResponse struct {
	Pending int `json:"pending"`
}
