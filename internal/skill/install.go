package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrNotFound reports that no skill has the name asked for.
var ErrNotFound = errors.New("skill not found")

// ErrBroken reports that a skill's directory no longer matches its row, so it
// cannot be enabled until it is installed again.
var ErrBroken = errors.New("skill is broken")

// InvalidError is an install the user's zip or its contents caused: not a zip,
// over a limit, no usable SKILL.md, or a credential in a file. Nothing else
// about an install is the user's to fix.
type InvalidError struct{ Err error }

// Invalid marks err as the fault of the uploaded archive.
func Invalid(err error) error { return &InvalidError{Err: err} }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// Broken reasons stored on a row whose directory no longer matches it.
const (
	BrokenMissing = "directory missing"
	BrokenChanged = "directory changed since install"
)

// FileInfo is one file of an installed skill, as listed in the install screen.
type FileInfo struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}

// ScreenResult is the install screen's finding, kept with the row. P6-05
// fills Spans; until then every install stores Unscanned. It is not part of
// the files.
type ScreenResult struct {
	Spans     []ScreenSpan `json:"spans"`
	MaxScore  float64      `json:"max_score"`
	Unscanned bool         `json:"unscanned"`
}

// ScreenSpan is one screened window at or over the threshold. Field is
// "description" or "body"; Start and End are byte offsets into the stored
// text, as the memory spans are.
type ScreenSpan struct {
	Field string  `json:"field"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// Record is a skill's row. Enabled is the user's choice; Broken, when set,
// means the skill must not be loaded whatever Enabled says.
type Record struct {
	Skill
	Enabled     bool
	SHA         string
	InstalledAt time.Time
	Files       []FileInfo
	Screen      *ScreenResult
	Broken      string
}

// Loadable is whether the skill may be offered to agents.
func (r Record) Loadable() bool { return r.Enabled && r.Broken == "" }

// Volume is the skills volume's fixed operations; skill/volume implements it.
type Volume interface {
	Stage(ctx context.Context, name string, files []File) error
	HashStaged(ctx context.Context, name string) (string, error)
	Replace(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) error
	// Hash returns fs.ErrNotExist for a skill that is not installed.
	Hash(ctx context.Context, name string) (string, error)
	// List names the entries of the volume that are not working directories.
	List(ctx context.Context) ([]string, error)
}

// Index stores skill rows. The store implements it.
type Index interface {
	// UpsertSkill writes r as a new row, replacing any row of that name. The
	// row is disabled and sound whatever r says: enabling is a separate act.
	UpsertSkill(ctx context.Context, r Record) error
	// GetSkill returns the row, or ErrNotFound.
	GetSkill(ctx context.Context, name string) (Record, error)
	ListSkills(ctx context.Context) ([]Record, error)
	DeleteSkill(ctx context.Context, name string) error
	// SetSkillEnabled sets a row's enabled flag, or returns ErrNotFound.
	SetSkillEnabled(ctx context.Context, name string, enabled bool) error
	// SetSkillBroken sets a row's broken reason, or clears it with "".
	SetSkillBroken(ctx context.Context, name, reason string) error
}

// Screener is the install screen's hook (P6-05). It sees the parsed skill and
// its files before anything is written and returns what to store with the
// row; an error rejects the install. It never enables.
type Screener func(ctx context.Context, a Archive, s Skill) (*ScreenResult, error)

// Unscanned is the screener used until P6-05: it stores an unscanned result.
func Unscanned(context.Context, Archive, Skill) (*ScreenResult, error) {
	return &ScreenResult{Unscanned: true}, nil
}

// Installer is the single writer of installed skills. Each install, delete
// and reconcile is one critical section: stage, verify, swap the directory,
// then write the row. A crash or failure between the swap and the row leaves
// the directory ahead of the row, which Reconcile reports and never adopts.
type Installer struct {
	mu     sync.Mutex
	volume Volume
	index  Index
	now    func() time.Time
	screen Screener
}

// NewInstaller returns the installer for a volume and its index.
func NewInstaller(v Volume, idx Index) *Installer {
	return &Installer{volume: v, index: idx, now: time.Now, screen: Unscanned}
}

// SetScreener replaces the install screen.
func (in *Installer) SetScreener(s Screener) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.screen = s
}

// InstallResult is what an install did. Unchanged is true when the same tree
// was already installed, in which case nothing was written.
type InstallResult struct {
	Record    Record
	Unchanged bool
}

// Install installs the skill in a zip. The row is created disabled. A
// reinstall of an installed name with identical contents is a no-op; with
// different contents it replaces directory and row and returns to disabled.
// A broken row with identical contents is repaired by reinstalling.
func (in *Installer) Install(ctx context.Context, zip []byte) (InstallResult, error) {
	arch, err := ReadZip(bytes.NewReader(zip), int64(len(zip)))
	if err != nil {
		return InstallResult{}, Invalid(err)
	}
	sk, err := arch.Skill()
	if err != nil {
		return InstallResult{}, Invalid(err)
	}
	want := TreeHash(arch.Files)

	in.mu.Lock()
	defer in.mu.Unlock()

	old, err := in.index.GetSkill(ctx, sk.Name)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return InstallResult{}, fmt.Errorf("read skill row: %w", err)
	}
	if exists && old.Broken == "" && old.SHA == want {
		return InstallResult{Record: old, Unchanged: true}, nil
	}
	screen, err := in.screen(ctx, arch, sk)
	if err != nil {
		return InstallResult{}, err
	}
	if err := in.volume.Stage(ctx, sk.Name, arch.Files); err != nil {
		return InstallResult{}, fmt.Errorf("stage skill: %w", err)
	}
	if got, err := in.volume.HashStaged(ctx, sk.Name); err != nil {
		return InstallResult{}, fmt.Errorf("hash staged skill: %w", err)
	} else if got != want {
		return InstallResult{}, errors.New("staged skill does not match the archive")
	}
	if err := in.volume.Replace(ctx, sk.Name); err != nil {
		return InstallResult{}, fmt.Errorf("install skill directory: %w", err)
	}
	if got, err := in.volume.Hash(ctx, sk.Name); err != nil {
		return InstallResult{}, fmt.Errorf("hash installed skill: %w", err)
	} else if got != want {
		return InstallResult{}, errors.New("installed skill does not match the archive")
	}
	rec := Record{Skill: sk, SHA: want, InstalledAt: in.now().UTC(), Screen: screen}
	for _, f := range arch.Files {
		rec.Files = append(rec.Files, FileInfo{Path: f.Path, Size: len(f.Data)})
	}
	if err := in.index.UpsertSkill(ctx, rec); err != nil {
		return InstallResult{}, fmt.Errorf("record skill: %w", err)
	}
	return InstallResult{Record: rec}, nil
}

// SetEnabled enables or disables a skill. check runs under the installer's
// lock with every row as it would be after the change, so two enables cannot
// both pass a budget that only one fits in; an error from it refuses the
// change. A broken skill cannot be enabled. Changing nothing is not an error.
func (in *Installer) SetEnabled(ctx context.Context, name string, enabled bool, check func(after []Record) error) (Record, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	rec, err := in.index.GetSkill(ctx, name)
	if err != nil {
		return Record{}, err
	}
	if rec.Enabled == enabled {
		return rec, nil
	}
	if enabled {
		if rec.Broken != "" {
			return Record{}, fmt.Errorf("%w: %s", ErrBroken, rec.Broken)
		}
		if check != nil {
			rows, err := in.index.ListSkills(ctx)
			if err != nil {
				return Record{}, fmt.Errorf("list skills: %w", err)
			}
			for i := range rows {
				if rows[i].Name == name {
					rows[i].Enabled = true
				}
			}
			if err := check(rows); err != nil {
				return Record{}, err
			}
		}
	}
	if err := in.index.SetSkillEnabled(ctx, name, enabled); err != nil {
		return Record{}, err
	}
	rec.Enabled = enabled
	return rec, nil
}

// Delete removes a skill's directory and then its row. A failure between the
// two leaves a row without a directory, which Reconcile marks broken.
func (in *Installer) Delete(ctx context.Context, name string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if _, err := in.index.GetSkill(ctx, name); err != nil {
		return err
	}
	if err := in.volume.Remove(ctx, name); err != nil {
		return fmt.Errorf("remove skill directory: %w", err)
	}
	return in.index.DeleteSkill(ctx, name)
}

// Report is what a reconcile found. Broken names rows newly marked broken,
// Repaired rows whose directory matches again, Unknown directories on the
// volume that no row owns.
type Report struct {
	Broken   []string
	Repaired []string
	Unknown  []string
}

// Reconcile compares every row with the volume at start. The directory's hash
// decides: a row whose directory is missing (including a crash inside the
// volume's Replace, which leaves the name missing and the old tree aside) or
// whose hash differs is marked broken and not loaded, never dropped or
// re-hashed to fit. An unknown directory is reported and never adopted: only
// an install, which parses, validates and records it, makes a skill.
func (in *Installer) Reconcile(ctx context.Context) (Report, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	var rep Report
	rows, err := in.index.ListSkills(ctx)
	if err != nil {
		return rep, fmt.Errorf("list skills: %w", err)
	}
	dirs, err := in.volume.List(ctx)
	if err != nil {
		return rep, fmt.Errorf("list skills volume: %w", err)
	}
	known := map[string]bool{}
	for _, r := range rows {
		known[r.Name] = true
		reason := ""
		got, err := in.volume.Hash(ctx, r.Name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			reason = BrokenMissing
		case err != nil:
			return rep, fmt.Errorf("hash skill %s: %w", r.Name, err)
		case got != r.SHA:
			reason = BrokenChanged
		}
		if reason == r.Broken {
			continue
		}
		if err := in.index.SetSkillBroken(ctx, r.Name, reason); err != nil {
			return rep, fmt.Errorf("mark skill %s: %w", r.Name, err)
		}
		if reason == "" {
			rep.Repaired = append(rep.Repaired, r.Name)
		} else {
			rep.Broken = append(rep.Broken, r.Name)
		}
	}
	for _, d := range dirs {
		if !known[d] {
			rep.Unknown = append(rep.Unknown, d)
		}
	}
	slices.Sort(rep.Unknown)
	return rep, nil
}

// TreeHash is the hash the volume computes over an installed tree: SHA-256
// over the sorted lines "<octal mode> <sha256 of contents> ./<path>", where
// the mode is 755 for an executable file and 644 otherwise.
func TreeHash(files []File) string {
	type line struct{ path, text string }
	lines := make([]line, 0, len(files))
	for _, f := range files {
		mode := "644"
		if f.Mode&0o111 != 0 {
			mode = "755"
		}
		sum := sha256.Sum256(f.Data)
		p := "./" + f.Path
		lines = append(lines, line{p, mode + " " + hex.EncodeToString(sum[:]) + " " + p + "\n"})
	}
	slices.SortFunc(lines, func(a, b line) int { return strings.Compare(a.path, b.path) })
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.text)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
