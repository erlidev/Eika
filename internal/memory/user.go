package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrStale rejects a user change made against content that has since
	// changed: the caller's content_sha is not the stored file's.
	ErrStale = errors.New("memory changed since it was read")
	// ErrNotActive rejects pinning a memory that is not active: only active
	// memories reach the section, so only they may hold a pin.
	ErrNotActive = errors.New("only an active memory can be pinned")
	// ErrNotPending rejects accepting or rejecting a memory that is not
	// pending: it was already answered, or never needed an answer.
	ErrNotPending = errors.New("memory is not pending")
)

// MaxListLimit bounds a page of the memory list.
const MaxListLimit = 101

// ListQuery pages the memory list, newest update first with name breaking
// ties. State and Type filter when set; Pinned filters when non-nil. A page
// starts after the row (AfterUpdated, AfterName) when AfterName is set.
type ListQuery struct {
	State, Type  string
	Pinned       *bool
	AfterUpdated time.Time
	AfterName    string
	Limit        int
}

// Summary is a memory's row without its body.
type Summary struct {
	File
	ContentSHA string
}

// ContentSHA is the digest the index keeps of a memory file's bytes, and the
// precondition of a user change.
func ContentSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func checkSHA(data []byte, ifSHA string) error {
	if ifSHA != "" && ContentSHA(data) != ifSHA {
		return ErrStale
	}
	return nil
}

// ValidName reports whether name can be a memory name.
func ValidName(name string) bool { return slugPattern.MatchString(name) }

// List returns one bounded page of memories from the index.
func (w *Writer) List(ctx context.Context, q ListQuery) ([]Summary, error) {
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	q.Limit = min(q.Limit, MaxListLimit)
	return w.index.ListMemories(ctx, q)
}

// Get returns a memory's index row, which carries its screen verdict and
// archive reason as well as its content_sha.
func (w *Writer) Get(ctx context.Context, name string) (Record, error) {
	return w.index.GetMemory(ctx, name)
}

// Edit is a user's change to a memory's text or type. The values are final:
// the caller has merged them over the stored memory and screened the text. A
// pending memory stays pending, an active one is pending only when State says
// so (the screen flagged the edit), and an archived one stays archived. A
// memory that becomes pending is unpinned, so accepting it later never needs a
// pin-budget decision. Screen is the verdict behind a pending edit.
type Edit struct {
	Name, IfSHA                      string
	Description, Type, Body, Expires string
	State                            string // "" keeps the stored state
	Screen                           *ScreenResult
	Count                            CountFunc
}

// Edit applies a user edit under the writer's lock: stale check, pin budget
// when the memory stays pinned, file write, commit, then the row.
func (w *Writer) Edit(ctx context.Context, e Edit) (File, error) {
	return w.mutate(ctx, e.Name, e.IfSHA, func(old File) (File, *ScreenResult, string, error) {
		next := old
		next.Description, next.Type, next.Body, next.Expires = e.Description, e.Type, e.Body, e.Expires
		switch {
		case e.State == "" || e.State == old.State:
		case old.State == "active" && e.State == "pending":
			next.State = "pending"
		default:
			return File{}, nil, "", fmt.Errorf("an edit cannot change state from %s to %s", old.State, e.State)
		}
		if next.State == "pending" {
			next.Pinned = false
		}
		if err := w.checkPins(ctx, e.Count, next); err != nil {
			return File{}, nil, "", err
		}
		screen := e.Screen
		if next.State != "pending" {
			screen = nil
		}
		return next, screen, "Edit memory", nil
	})
}

// SetPinned pins or unpins a memory. A pin needs an active memory and must
// leave the pinned set within MaxPins entries and SectionBudgetTokens, counted
// with count, which must be exact.
func (w *Writer) SetPinned(ctx context.Context, name string, pinned bool, ifSHA string, count CountFunc) (File, error) {
	return w.mutate(ctx, name, ifSHA, func(old File) (File, *ScreenResult, string, error) {
		next := old
		next.Pinned = pinned
		if pinned && old.State != "active" {
			return File{}, nil, "", ErrNotActive
		}
		if err := w.checkPins(ctx, count, next); err != nil {
			return File{}, nil, "", err
		}
		msg := "Pin memory"
		if !pinned {
			msg = "Unpin memory"
		}
		return next, nil, msg, nil
	})
}

// Purge removes a memory's file and its index row in one commit. Git history
// still holds the text, as do old sessions and backups; this does not rewrite
// history. No screen applies: nothing is written.
func (w *Writer) Purge(ctx context.Context, name, ifSHA string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, data, err := w.load(ctx, name)
	if err != nil {
		return err
	}
	if err := checkSHA(data, ifSHA); err != nil {
		return err
	}
	if err := w.files.Remove(ctx, name, "Purge memory: "+name); err != nil {
		return fmt.Errorf("remove memory: %w", err)
	}
	if err := w.index.DeleteMemory(ctx, name); err != nil {
		return fmt.Errorf("remove memory row: %w", err)
	}
	w.changed(ctx, name, "")
	return nil
}

// checkPins validates a memory that will be stored pinned against the pinned
// set. Nothing is counted unless next is pinned, active and unexpired, but a
// pinned next with no counter is refused rather than waved through.
func (w *Writer) checkPins(ctx context.Context, count CountFunc, next File) error {
	if !next.Pinned {
		return nil
	}
	if count == nil {
		return ErrNotExact
	}
	others, err := w.index.PinnedMemories(ctx)
	if err != nil {
		return fmt.Errorf("read pinned memories: %w", err)
	}
	return CheckPinBudget(ctx, count, others, next, w.now())
}

// mutate loads a memory under the lock, checks its content_sha, lets fn make
// the next version, and stores it with the new updated time.
func (w *Writer) mutate(ctx context.Context, name, ifSHA string, fn func(old File) (File, *ScreenResult, string, error)) (File, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old, oldData, err := w.load(ctx, name)
	if err != nil {
		return File{}, err
	}
	if err := checkSHA(oldData, ifSHA); err != nil {
		return File{}, err
	}
	next, screen, message, err := fn(old)
	if err != nil {
		return File{}, err
	}
	next.Updated = w.now().UTC().Format(time.RFC3339)
	res, err := w.store(ctx, next, old, oldData, true, screen, "", message)
	return res.File, err
}

// Accept is the user's answer that a pending memory is good: it becomes
// active, with the text and type the user settled on. The values are final:
// the caller has merged any edit over the stored memory and run the credential
// check. A flagged text is promoted on purpose, so no screen verdict is kept.
// It fails with ErrNotPending for a memory that is active or archived, which
// is how a repeated answer is refused, and with ErrStale unless ifSHA is the
// stored file's content_sha.
func (w *Writer) Accept(ctx context.Context, e Edit) (File, error) {
	return w.mutate(ctx, e.Name, e.IfSHA, func(old File) (File, *ScreenResult, string, error) {
		if old.State != "pending" {
			return File{}, nil, "", ErrNotPending
		}
		next := old
		next.Description, next.Type, next.Body, next.Expires = e.Description, e.Type, e.Body, e.Expires
		next.State = "active"
		return next, nil, "Accept memory", nil
	})
}

// Reject is the user's answer that a pending memory is not wanted: it is
// archived with reason, and its screen verdict is kept. The file stays in git
// history. It fails like Accept for a memory that is not pending.
func (w *Writer) Reject(ctx context.Context, name, reason, ifSHA string) (File, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old, oldData, err := w.load(ctx, name)
	if err != nil {
		return File{}, err
	}
	if err := checkSHA(oldData, ifSHA); err != nil {
		return File{}, err
	}
	if old.State != "pending" {
		return File{}, ErrNotPending
	}
	next := old
	next.State = "archived"
	next.Updated = w.now().UTC().Format(time.RFC3339)
	res, err := w.store(ctx, next, old, oldData, true, nil, reason, "Reject memory: "+oneLine(reason))
	return res.File, err
}
