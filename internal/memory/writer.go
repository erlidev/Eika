package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits on what a caller may ask of the writer.
const (
	MaxQueryRunes = 200
	DefaultLimit  = 10
	MaxLimit      = 50
	snippetRunes  = 200
)

// ErrNotFound reports that no memory has the name asked for.
var ErrNotFound = errors.New("memory not found")

// Files is the memory repository: the only place memory files are read,
// written and committed. internal/memory/repo.Repository implements it.
type Files interface {
	Read(ctx context.Context, name string) ([]byte, error)
	Write(ctx context.Context, name string, data []byte) error
	Commit(ctx context.Context, name string) error
}

// Screen is what Sentry's write check found in a pending memory, kept with
// its index row for the Inbox. It is not part of the file.
type Screen struct {
	Spans     []ScreenSpan `json:"spans"`
	MaxScore  float64      `json:"max_score"`
	Unscanned bool         `json:"unscanned"`
}

// ScreenSpan is one screened window at or over the threshold. Field is
// "description" or "body"; Start and End are byte offsets into that field
// with invisible characters removed.
type ScreenSpan struct {
	Field string  `json:"field"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// Record is a memory's row in the index. Body is the file's body. Screen is
// set for a memory written pending and kept when it is archived; Reason is
// why it was archived.
type Record struct {
	File
	ContentSHA string
	Screen     *Screen
	Reason     string
}

// Hit is one search result. Snippet is the start of the body.
type Hit struct {
	Name, Description, Type, State string
	Pinned                         bool
	Snippet                        string
	Rank                           float64
}

// Query is a full-text search. Archived memories are left out unless asked
// for. Type, when set, limits the result to one type.
type Query struct {
	Text            string
	Type            string
	IncludeArchived bool
	Limit           int
}

// Index stores memory rows. It is implemented by the store.
type Index interface {
	// UpsertMemory inserts or replaces the row named r.Name.
	UpsertMemory(ctx context.Context, r Record) error
	// SearchMemories ranks memories by full-text match. SnippetRunes bounds
	// the body prefix returned.
	SearchMemories(ctx context.Context, q Query, snippetRunes int) ([]Hit, error)
}

// Request creates or replaces a memory. Source cites where it was learned.
// State is "active" unless the caller's screen decided otherwise; Screen is
// the verdict behind a pending state.
type Request struct {
	Name, Description, Type, Body, Expires, Source, State string
	Replace                                               bool
	Screen                                                *Screen
}

// Result is the memory as stored. Existing is true when a name collision
// without Replace left the stored memory untouched and returned it.
type Result struct {
	File     File
	Existing bool
}

// Writer is the single writer of memories. Each write is one critical
// section: file write, git commit, then index row, so concurrent callers
// cannot interleave them. A crash between steps leaves a file the index does
// not match, which recovery (P5-04) repairs from the files.
type Writer struct {
	mu    sync.Mutex
	files Files
	index Index
	now   func() time.Time
}

// NewWriter returns the writer for a repository and its index.
func NewWriter(files Files, index Index) *Writer {
	return &Writer{files: files, index: index, now: time.Now}
}

// Write creates a memory. If the name is taken and Replace is false the
// stored memory is returned unchanged with Existing set, so the caller merges
// instead. With Replace it overwrites, keeping the created time and pin.
func (w *Writer) Write(ctx context.Context, req Request) (Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	state := req.State
	if state == "" {
		state = "active"
	}
	now := w.now().UTC().Format(time.RFC3339)
	next := File{
		Name: req.Name, Description: req.Description, Type: req.Type,
		Source: req.Source, Created: now, Updated: now, Expires: req.Expires,
		State: state, Body: req.Body,
	}
	old, oldData, err := w.load(ctx, req.Name)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}
	if exists {
		if !req.Replace {
			return Result{File: old, Existing: true}, nil
		}
		next.Created, next.Pinned = old.Created, old.Pinned
	}
	if err := next.Validate(req.Name + ".md"); err != nil {
		return Result{}, err
	}
	return w.store(ctx, next, old, oldData, exists, req.Screen, "")
}

// Archive moves a memory to state archived and records why in the index. The
// file stays in git history.
func (w *Writer) Archive(ctx context.Context, name, reason string) (File, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old, oldData, err := w.load(ctx, name)
	if err != nil {
		return File{}, err
	}
	next := old
	next.State = "archived"
	next.Updated = w.now().UTC().Format(time.RFC3339)
	res, err := w.store(ctx, next, old, oldData, true, nil, reason)
	return res.File, err
}

// store renders next, and when it differs from what is stored writes,
// commits and indexes it. An unchanged file is only re-indexed, which also
// repairs a missing or stale row.
func (w *Writer) store(ctx context.Context, next, old File, oldData []byte, exists bool, screen *Screen, reason string) (Result, error) {
	if exists && sameContent(next, old) {
		next.Updated = old.Updated
	}
	data, err := next.Render(next.Name + ".md")
	if err != nil {
		return Result{}, err
	}
	if !exists || string(data) != string(oldData) {
		if err := w.files.Write(ctx, next.Name, data); err != nil {
			return Result{}, fmt.Errorf("write memory file: %w", err)
		}
		if err := w.files.Commit(ctx, next.Name); err != nil {
			return Result{}, fmt.Errorf("commit memory: %w", err)
		}
	}
	sum := sha256.Sum256(data)
	if err := w.index.UpsertMemory(ctx, Record{File: next, ContentSHA: hex.EncodeToString(sum[:]), Screen: screen, Reason: reason}); err != nil {
		return Result{}, fmt.Errorf("index memory: %w", err)
	}
	return Result{File: next}, nil
}

// sameContent reports whether two files differ at most in Updated.
func sameContent(a, b File) bool {
	a.Updated = b.Updated
	return a == b
}

// load reads and parses a stored memory, or returns ErrNotFound.
func (w *Writer) load(ctx context.Context, name string) (File, []byte, error) {
	data, err := w.files.Read(ctx, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return File{}, nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return File{}, nil, fmt.Errorf("read memory: %w", err)
	}
	f, err := Parse(name+".md", data)
	if err != nil {
		return File{}, nil, fmt.Errorf("stored memory %s: %w", name, err)
	}
	return f, data, nil
}

// Read returns one memory from its file.
func (w *Writer) Read(ctx context.Context, name string) (File, error) {
	f, _, err := w.load(ctx, name)
	return f, err
}

// Search finds memories by full-text match. The query and limit are bounded.
func (w *Writer) Search(ctx context.Context, q Query) ([]Hit, error) {
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if utf8.RuneCountInString(q.Text) > MaxQueryRunes {
		return nil, fmt.Errorf("query must be at most %d characters", MaxQueryRunes)
	}
	switch q.Type {
	case "", "user", "preference", "person", "fact", "procedure":
	default:
		return nil, fmt.Errorf("invalid type")
	}
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	q.Limit = min(q.Limit, MaxLimit)
	return w.index.SearchMemories(ctx, q, snippetRunes)
}
