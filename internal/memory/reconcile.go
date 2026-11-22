package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"
)

// Screen decides the state of a memory whose file changed outside the
// writer, before it is committed or indexed. It is given the file as found
// and returns the state to store. It never raises a state: a pending or
// archived file keeps its state whatever Screen says.
//
// P5-05 hook: Sentry's memory-write check replaces HoldPending here through
// Writer.SetScreen. Until it exists every outside edit is held pending, so
// nothing unscreened becomes active.
type Screen func(ctx context.Context, f File) (state string, err error)

// HoldPending is the screen used until P5-05: it holds every edit pending.
func HoldPending(context.Context, File) (string, error) { return "pending", nil }

// SetScreen replaces the screen applied to files changed outside the writer.
func (w *Writer) SetScreen(s Screen) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.screen = s
}

// Report counts what a reconcile did. Invalid names files that do not parse
// as memories: they are left on disk, unindexed and unserved.
type Report struct {
	Reindexed int // rows written or rewritten from files
	Committed int // uncommitted files committed
	Restored  int // deleted files restored from the last commit
	Removed   int // rows dropped because no file stands behind them
	Held      int // outside edits made pending by the screen
	Invalid   []string
}

// Reconcile repairs the index and the repository after a restart or a failure
// between the file write, commit and row update. The files are the truth.
//
//   - A file missing from the working tree is restored from its last commit;
//     with no commit behind it, its row is removed.
//   - A row with no file is removed.
//   - An uncommitted file is committed. If its content is not what the row
//     holds it was changed outside the writer (or its own write was cut
//     short), so Screen decides its state first; an active file is made
//     pending until P5-05's screen clears it.
//   - A committed file whose bytes differ from the row's content_sha, or with
//     no row, is indexed as it is: git holds it, so it is the writer's own.
//   - Pending and archived states, created, pinned, and commits are kept.
//
// It goes through the same writer lock and the same file, commit and row
// steps as Write, so there is no second write path. It can be run again
// after any failure.
func (w *Writer) Reconcile(ctx context.Context) (Report, error) { return w.reconcile(ctx, false) }

// Reindex is Reconcile that also rewrites every row, matching or not, which
// is the full rebuild behind `make memory-reindex`.
func (w *Writer) Reindex(ctx context.Context) (Report, error) { return w.reconcile(ctx, true) }

func (w *Writer) reconcile(ctx context.Context, full bool) (Report, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var rep Report

	names, err := w.files.List(ctx)
	if err != nil {
		return rep, fmt.Errorf("list memory files: %w", err)
	}
	dirtyNames, err := w.files.Uncommitted(ctx)
	if err != nil {
		return rep, fmt.Errorf("memory status: %w", err)
	}
	rows, err := w.index.MemorySHAs(ctx)
	if err != nil {
		return rep, fmt.Errorf("read index: %w", err)
	}
	dirty := map[string]bool{}
	for _, n := range dirtyNames {
		dirty[n] = true
	}
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	// Candidates that are tracked but gone from the tree, or only in the
	// index, come back from git or lose their row.
	missing := map[string]bool{}
	for n := range rows {
		missing[n] = !present[n]
	}
	for n := range dirty {
		missing[n] = missing[n] || !present[n]
	}
	for n, gone := range missing {
		if !gone {
			continue
		}
		ok, err := w.files.Restore(ctx, n)
		if err != nil {
			return rep, fmt.Errorf("restore memory %s: %w", n, err)
		}
		if ok {
			rep.Restored++
			names = append(names, n)
			delete(dirty, n)
			continue
		}
		if _, has := rows[n]; has {
			if err := w.index.DeleteMemory(ctx, n); err != nil {
				return rep, fmt.Errorf("remove memory row %s: %w", n, err)
			}
			rep.Removed++
		}
		delete(dirty, n)
	}
	slices.Sort(names)

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if err := w.reconcileFile(ctx, name, rows, dirty[name], full, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (w *Writer) reconcileFile(ctx context.Context, name string, rows map[string]string, dirty, full bool, rep *Report) error {
	data, err := w.files.Read(ctx, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // removed while reconciling
		}
		return fmt.Errorf("read memory %s: %w", name, err)
	}
	f, err := Parse(name+".md", data)
	if err != nil {
		// Unparseable content is never indexed or served.
		rep.Invalid = append(rep.Invalid, name)
		if _, has := rows[name]; has {
			if err := w.index.DeleteMemory(ctx, name); err != nil {
				return fmt.Errorf("remove memory row %s: %w", name, err)
			}
			rep.Removed++
		}
		return nil
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	rowSHA, hasRow := rows[name]
	if !dirty && hasRow && rowSHA == sha && !full {
		return nil
	}
	if dirty && (!hasRow || rowSHA != sha) {
		// Changed outside the writer, or its own write was cut short.
		state := f.State
		if state == "active" {
			state, err = w.screen(ctx, f)
			if err != nil || (state != "active" && state != "pending") {
				state = "pending"
			}
		}
		if state != f.State {
			f.State = state
			rep.Held++
			data, err = f.Render(name + ".md")
			if err != nil {
				return fmt.Errorf("render memory %s: %w", name, err)
			}
			if err := w.files.Write(ctx, name, data); err != nil {
				return fmt.Errorf("write memory file: %w", err)
			}
		}
	}
	if dirty {
		if err := w.files.Commit(ctx, name); err != nil {
			return fmt.Errorf("commit memory: %w", err)
		}
		rep.Committed++
	}
	if err := w.indexFile(ctx, f, data); err != nil {
		return err
	}
	rep.Reindexed++
	return nil
}
