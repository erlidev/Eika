package bench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Snapshot is what the server held when a run began: every container that
// ran then, the containers the run may touch whether or not they ran, and the
// GPU. A run is over when the server matches it again.
type Snapshot struct {
	// At is when it was taken, by the harness's clock.
	At time.Time `json:"at"`
	// Containers are the running containers and the ones named in the
	// options, in name order.
	Containers []Container `json:"containers"`
	// GPU is the GPU's memory and the processes holding it.
	GPU GPU `json:"gpu"`
}

// container returns the recorded container of that name.
func (s Snapshot) container(name string) (Container, bool) {
	for _, c := range s.Containers {
		if c.Name == name {
			return c, true
		}
	}
	return Container{}, false
}

// Journal is what a run keeps on this machine, on disk, so that a later run
// or `benchctl restore` can tell a bench left modified and put it back. It is
// written before each change to the server and removed once the server
// matches its snapshot again.
type Journal struct {
	// Run identifies the run; the lock and the container of a variant carry
	// it.
	Run string `json:"run"`
	// Host is the ssh host the run used.
	Host string `json:"host"`
	// StartedAt is when the run began.
	StartedAt time.Time `json:"started_at"`
	// Before is the snapshot to restore.
	Before Snapshot `json:"before"`
	// Chat and Tabby are the names of the two models the run may serve.
	Chat  string `json:"chat"`
	Tabby string `json:"tabby"`
	// Stopped are the containers the run stopped, in order.
	Stopped []string `json:"stopped,omitempty"`
	// Started are the containers the run started that were not running when
	// it began.
	Started []string `json:"started,omitempty"`
	// Created are the containers the run created.
	Created []string `json:"created,omitempty"`
}

// journalFile persists a Journal.
type journalFile struct {
	path string

	mu sync.Mutex
	j  Journal
}

// DefaultJournalPath is where a run keeps its journal: the user's cache
// directory, which does not depend on the working directory of the run.
func DefaultJournalPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find the cache directory: %w", err)
	}
	return filepath.Join(dir, "eika", "bench-journal.json"), nil
}

// ReadJournal returns the journal at path, or ErrNoJournal when there is none.
func ReadJournal(path string) (Journal, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Journal{}, ErrNoJournal
	}
	if err != nil {
		return Journal{}, fmt.Errorf("read the bench journal: %w", err)
	}
	var j Journal
	if err := json.Unmarshal(raw, &j); err != nil {
		return Journal{}, fmt.Errorf("decode the bench journal %s: %w", path, err)
	}
	return j, nil
}

// ErrNoJournal means no run left a journal.
var ErrNoJournal = errors.New("no bench journal")

// save writes the journal, atomically.
func (f *journalFile) save() error {
	raw, err := json.MarshalIndent(f.j, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the bench journal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("create the bench journal directory: %w", err)
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the bench journal: %w", err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("replace the bench journal: %w", err)
	}
	return nil
}

// update changes the journal under the lock and writes it. A run that cannot
// write its journal must not go on to change the server, so the caller
// returns the error before it acts.
func (f *journalFile) update(change func(*Journal)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.j)
	return f.save()
}

// get returns a copy of the journal.
func (f *journalFile) get() Journal {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.j
	j.Stopped, j.Started, j.Created = slices.Clone(j.Stopped), slices.Clone(j.Started), slices.Clone(j.Created)
	return j
}

// remove deletes the journal file.
func (f *journalFile) remove() error {
	if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the bench journal: %w", err)
	}
	return nil
}
