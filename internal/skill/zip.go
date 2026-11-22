package skill

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Limits on an uploaded skill archive.
const (
	MaxZipBytes      = 5 << 20
	MaxExpandedBytes = 20 << 20
	MaxFiles         = 200
	// MaxDepth is the most directory levels a file may sit below the skill
	// directory.
	MaxDepth = 5
)

// File is one regular file of a skill, with a path relative to the skill
// directory. Mode is 0o755 for an executable under scripts/ and 0o644
// otherwise.
type File struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

// Archive is a skill read from a zip. Dir is the single top-level directory
// the files were in, or "" for a flat zip. Files are sorted by path.
type Archive struct {
	Dir   string
	Files []File
}

// ReadZip reads a skill archive of size bytes, enforcing the limits and
// rejecting entries that escape the skill directory. It accepts a flat zip
// (SKILL.md at the top) or one top-level directory holding SKILL.md.
// Directory entries are dropped; symlinks and other non-regular files are
// refused.
func ReadZip(r io.ReaderAt, size int64) (Archive, error) {
	if size > MaxZipBytes {
		return Archive{}, fmt.Errorf("zip is over %d bytes", MaxZipBytes)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Archive{}, fmt.Errorf("open zip: %w", err)
	}
	if len(zr.File) > MaxFiles {
		return Archive{}, fmt.Errorf("zip has more than %d entries", MaxFiles)
	}

	type entry struct {
		parts []string
		f     *zip.File
	}
	var entries []entry
	seen := map[string]bool{}
	var declared uint64
	for _, f := range zr.File {
		name := f.Name
		if !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") {
			return Archive{}, fmt.Errorf("unsafe path %q", name)
		}
		isDir := strings.HasSuffix(name, "/")
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		for _, p := range parts {
			if p == ".." || p == "." || p == "" {
				return Archive{}, fmt.Errorf("unsafe path %q", name)
			}
		}
		if parts[0] == "__MACOSX" {
			continue
		}
		mode := f.Mode()
		if isDir {
			continue
		}
		if !mode.IsRegular() {
			return Archive{}, fmt.Errorf("%q is not a regular file", name)
		}
		if seen[name] {
			return Archive{}, fmt.Errorf("duplicate path %q", name)
		}
		seen[name] = true
		declared += f.UncompressedSize64
		if declared > MaxExpandedBytes {
			return Archive{}, fmt.Errorf("zip expands to more than %d bytes", MaxExpandedBytes)
		}
		entries = append(entries, entry{parts, f})
	}

	// Find the skill root: top level, or the one directory that holds it.
	var out Archive
	strip := 0
	switch {
	case slices.ContainsFunc(entries, func(e entry) bool { return len(e.parts) == 1 && e.parts[0] == "SKILL.md" }):
		// A flat zip: the skill is the archive itself.
	default:
		tops := map[string]bool{}
		for _, e := range entries {
			tops[e.parts[0]] = true
		}
		if len(tops) != 1 {
			return Archive{}, fmt.Errorf("zip must hold one skill directory or SKILL.md at the top")
		}
		out.Dir = entries[0].parts[0]
		strip = 1
	}

	var total int64
	for _, e := range entries {
		rel := e.parts[strip:]
		if len(rel) == 0 {
			return Archive{}, fmt.Errorf("unsafe path %q", e.f.Name)
		}
		if len(rel)-1 > MaxDepth {
			return Archive{}, fmt.Errorf("%q is more than %d directories deep", e.f.Name, MaxDepth)
		}
		rc, err := e.f.Open()
		if err != nil {
			return Archive{}, fmt.Errorf("open %q: %w", e.f.Name, err)
		}
		// Read one byte past the budget: a header can lie about its size.
		budget := MaxExpandedBytes - total
		data, err := io.ReadAll(io.LimitReader(rc, budget+1))
		_ = rc.Close()
		if err != nil {
			return Archive{}, fmt.Errorf("read %q: %w", e.f.Name, err)
		}
		total += int64(len(data))
		if total > MaxExpandedBytes {
			return Archive{}, fmt.Errorf("zip expands to more than %d bytes", MaxExpandedBytes)
		}
		relPath := strings.Join(rel, "/")
		if relPath == "SKILL.md" && len(data) > MaxSkillFileBytes {
			return Archive{}, fmt.Errorf("SKILL.md is over %d bytes", MaxSkillFileBytes)
		}
		mode := fs.FileMode(0o644)
		if rel[0] == "scripts" && e.f.Mode()&0o111 != 0 {
			mode = 0o755
		}
		out.Files = append(out.Files, File{Path: relPath, Mode: mode, Data: data})
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	if _, ok := out.file("SKILL.md"); !ok {
		return Archive{}, fmt.Errorf("zip has no SKILL.md")
	}
	return out, nil
}

// Skill parses the archive's SKILL.md.
func (a Archive) Skill() (Skill, error) {
	f, _ := a.file("SKILL.md")
	return Parse(a.Dir, f.Data)
}

func (a Archive) file(p string) (File, bool) {
	i := sort.Search(len(a.Files), func(i int) bool { return a.Files[i].Path >= p })
	if i < len(a.Files) && a.Files[i].Path == p {
		return a.Files[i], true
	}
	return File{}, false
}
