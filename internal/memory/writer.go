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
	Commit(ctx context.Context, name, message string) error
	// List names the memory files in the working tree.
	List(ctx context.Context) ([]string, error)
	// Uncommitted names the memory files that are new, changed or deleted
	// relative to the last commit.
	Uncommitted(ctx context.Context) ([]string, error)
	// Restore brings a deleted file back from the last commit, and reports
	// false when no commit has it.
	Restore(ctx context.Context, name string) (bool, error)
	// Remove deletes the file and commits the deletion with message. The
	// file stays in git history. Removing a missing file is not an error.
	Remove(ctx context.Context, name, message string) error
}

// ScreenResult is what Sentry's write check found in a pending memory, kept with
// its index row for the Inbox. It is not part of the file.
type ScreenResult struct {
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
	Screen     *ScreenResult
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
	// MemorySHAs returns every row's content_sha by name.
	MemorySHAs(ctx context.Context) (map[string]string, error)
	// DeleteMemory removes the row named name, if there is one.
	DeleteMemory(ctx context.Context, name string) error
	// SearchMemories ranks memories by full-text match. SnippetRunes bounds
	// the body prefix returned.
	SearchMemories(ctx context.Context, q Query, snippetRunes int) ([]Hit, error)
	// ListMemories pages the rows, newest update first.
	ListMemories(ctx context.Context, q ListQuery) ([]Summary, error)
	// GetMemory returns one row, or ErrNotFound.
	GetMemory(ctx context.Context, name string) (Record, error)
	// PinnedMemories returns every pinned, active row.
	PinnedMemories(ctx context.Context) ([]File, error)
}

// Request creates or replaces a memory. Source cites where it was learned.
// State is "active" unless the caller's screen decided otherwise; Screen is
// the verdict behind a pending state.
type Request struct {
	Name, Description, Type, Body, Expires, Source, State string
	Replace                                               bool
	Screen                                                *ScreenResult
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
// not match, which Reconcile repairs from the files.
type Writer struct {
	mu    sync.Mutex
	files Files
	index Index
	now   func() time.Time
	// screen decides the state of a file changed outside the writer.
	screen Screen
	// onChange is told of each memory whose row was written or removed,
	// after the change is durable. It must not block.
	onChange ChangeFunc
}

// ChangeFunc reports a durable change of one memory: its name and its new
// state, empty when the memory was purged.
type ChangeFunc func(ctx context.Context, name, state string)

// SetOnChange sets the function told of each durable change.
func (w *Writer) SetOnChange(f ChangeFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onChange = f
}

func (w *Writer) changed(ctx context.Context, name, state string) {
	if w.onChange != nil {
		w.onChange(ctx, name, state)
	}
}

// NewWriter returns the writer for a repository and its index.
func NewWriter(files Files, index Index) *Writer {
	return &Writer{files: files, index: index, now: time.Now, screen: HoldPending}
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
	return w.store(ctx, next, old, oldData, exists, req.Screen, "", "Update memory")
}

// Archive moves a memory to state archived and records why in the index. The
// file stays in git history.
func (w *Writer) Archive(ctx context.Context, name, reason string) (File, error) {
	return w.ArchiveIf(ctx, name, reason, "")
}

// ArchiveIf is Archive that fails with ErrStale unless the stored file's
// content_sha is ifSHA. An empty ifSHA skips the check.
func (w *Writer) ArchiveIf(ctx context.Context, name, reason, ifSHA string) (File, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old, oldData, err := w.load(ctx, name)
	if err != nil {
		return File{}, err
	}
	if err := checkSHA(oldData, ifSHA); err != nil {
		return File{}, err
	}
	next := old
	next.State = "archived"
	next.Updated = w.now().UTC().Format(time.RFC3339)
	res, err := w.store(ctx, next, old, oldData, true, nil, reason, "Forget memory: "+oneLine(reason))
	return res.File, err
}

// store renders next, and when it differs from what is stored writes,
// commits and indexes it. An unchanged file is only re-indexed, which also
// repairs a missing or stale row.
func (w *Writer) store(ctx context.Context, next, old File, oldData []byte, exists bool, screen *ScreenResult, reason, message string) (Result, error) {
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
		if err := w.files.Commit(ctx, next.Name, message); err != nil {
			return Result{}, fmt.Errorf("commit memory: %w", err)
		}
	}
	if err := w.indexFile(ctx, next, data, screen, reason); err != nil {
		return Result{}, err
	}
	return Result{File: next}, nil
}

// indexFile writes the row for a file whose bytes are data.
func (w *Writer) indexFile(ctx context.Context, f File, data []byte, screen *ScreenResult, reason string) error {
	sum := sha256.Sum256(data)
	if err := w.index.UpsertMemory(ctx, Record{File: f, ContentSHA: hex.EncodeToString(sum[:]), Screen: screen, Reason: reason}); err != nil {
		return fmt.Errorf("index memory: %w", err)
	}
	w.changed(ctx, f.Name, f.State)
	return nil
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

// oneLine makes text safe for a commit subject.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
