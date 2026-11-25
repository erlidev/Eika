package skill

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSkill = "---\nname: pdf-forms\ndescription: Fill PDF forms. Use when asked.\n---\nBody\n"

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		in       string
		skip     bool
		warnings int
		check    func(t *testing.T, s Skill)
	}{
		{name: "valid", dir: "pdf-forms", in: goodSkill, check: func(t *testing.T, s Skill) {
			if s.Name != "pdf-forms" || s.Body != "Body\n" {
				t.Errorf("got %q body %q", s.Name, s.Body)
			}
		}},
		{name: "flat zip has no directory to compare", in: goodSkill},
		{name: "name differs from directory", dir: "forms", in: goodSkill, warnings: 1},
		{name: "unquoted colon is retried quoted", dir: "pdf-forms", warnings: 1,
			in: "---\nname: pdf-forms\ndescription: Fill forms: flatten them too\n---\n",
			check: func(t *testing.T, s Skill) {
				if s.Description != "Fill forms: flatten them too" {
					t.Errorf("description %q", s.Description)
				}
			}},
		{name: "missing name uses directory", dir: "pdf-forms", warnings: 1,
			in: "---\ndescription: Fill forms.\n---\n"},
		{name: "unknown field and numeric metadata", dir: "pdf-forms", warnings: 1,
			in: "---\nname: pdf-forms\ndescription: d\nversion: 2\nmetadata:\n  v: 1.0\n---\n",
			check: func(t *testing.T, s Skill) {
				if s.Metadata["v"] != "1" && s.Metadata["v"] != "1.0" {
					t.Errorf("metadata %v", s.Metadata)
				}
			}},
		{name: "metadata that is not a map", dir: "pdf-forms", warnings: 1,
			in: "---\nname: pdf-forms\ndescription: d\nmetadata: [a, b]\n---\n"},
		{name: "description too long", dir: "pdf-forms", warnings: 1,
			in: "---\nname: pdf-forms\ndescription: " + strings.Repeat("a", 1025) + "\n---\n"},
		{name: "allowed-tools stored", dir: "pdf-forms",
			in: "---\nname: pdf-forms\ndescription: d\nallowed-tools: Bash(git:*) Read\n---\n",
			check: func(t *testing.T, s Skill) {
				if s.AllowedTools != "Bash(git:*) Read" {
					t.Errorf("allowed-tools %q", s.AllowedTools)
				}
			}},
		{name: "CRLF line endings", dir: "pdf-forms", in: strings.ReplaceAll(goodSkill, "\n", "\r\n")},
		{name: "long body", dir: "pdf-forms", warnings: 1,
			in: goodSkill + strings.Repeat("line\n", 501)},
		{name: "missing description", skip: true, in: "---\nname: pdf-forms\n---\nBody\n"},
		{name: "unparseable YAML", skip: true, in: "---\nname: [pdf\ndescription: d\n---\n"},
		{name: "no frontmatter", skip: true, in: "# Just markdown\n"},
		{name: "unclosed frontmatter", skip: true, in: "---\nname: a\ndescription: d\n"},
		{name: "unsafe name", skip: true, in: "---\nname: ../etc\ndescription: d\n---\n"},
		{name: "double hyphen name", skip: true, in: "---\nname: a--b\ndescription: d\n---\n"},
		{name: "over 64 KiB", skip: true, in: goodSkill + strings.Repeat("x", MaxSkillFileBytes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(tt.dir, []byte(tt.in))
			if (err != nil) != tt.skip {
				t.Fatalf("err = %v, skip = %v", err, tt.skip)
			}
			if tt.skip {
				return
			}
			if len(s.Warnings) != tt.warnings {
				t.Errorf("warnings = %q, want %d", s.Warnings, tt.warnings)
			}
			if tt.check != nil {
				tt.check(t, s)
			}
		})
	}
}

// The fixtures other packages read must stay valid skills.
func TestFixtures(t *testing.T) {
	for _, name := range []string{"invoice-totals", "notes-formatter"} {
		data, err := os.ReadFile(filepath.Join("testdata", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		s, err := Parse(name, data)
		if err != nil || len(s.Warnings) != 0 || s.Name != name {
			t.Errorf("%s: %+v, %v", name, s, err)
		}
	}
}

type zipEntry struct {
	name string
	body string
	mode fs.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadZipFixture(t *testing.T) {
	root := filepath.Join("testdata", "invoice-totals")
	var entries []zipEntry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		entries = append(entries, zipEntry{"invoice-totals/" + filepath.ToSlash(rel), string(data), info.Mode()})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data := makeZip(t, entries...)
	a, err := ReadZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.Skill()
	if err != nil || a.Dir != "invoice-totals" || len(s.Warnings) != 0 {
		t.Fatalf("dir %q skill %+v err %v", a.Dir, s, err)
	}
	modes := map[string]fs.FileMode{}
	for _, f := range a.Files {
		modes[f.Path] = f.Mode
	}
	if len(modes) != 3 || modes["scripts/totals.py"] != 0o755 || modes["SKILL.md"] != 0o644 || modes["references/rounding.md"] != 0o644 {
		t.Errorf("files %v", modes)
	}
}

func TestReadZip(t *testing.T) {
	skillMD := zipEntry{"SKILL.md", goodSkill, 0o644}
	many := []zipEntry{skillMD}
	for i := 0; i < MaxFiles; i++ {
		many = append(many, zipEntry{"f" + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + ".txt", "", 0o644})
	}
	tests := []struct {
		name    string
		entries []zipEntry
		ok      bool
	}{
		{name: "flat", ok: true, entries: []zipEntry{skillMD, {"scripts/run.sh", "#!/bin/sh", 0o755}}},
		{name: "directory entries and macOS metadata are dropped", ok: true, entries: []zipEntry{
			{"s/", "", fs.ModeDir | 0o755}, {"s/SKILL.md", goodSkill, 0o644}, {"__MACOSX/s/._SKILL.md", "x", 0o644}}},
		{name: "script without the exec bit stays plain", ok: true, entries: []zipEntry{skillMD, {"scripts/a.py", "x", 0o644}}},
		{name: "dot dot", entries: []zipEntry{skillMD, {"../evil", "x", 0o644}}},
		{name: "dot dot inside", entries: []zipEntry{skillMD, {"a/../../evil", "x", 0o644}}},
		{name: "absolute", entries: []zipEntry{skillMD, {"/etc/evil", "x", 0o644}}},
		{name: "backslash", entries: []zipEntry{skillMD, {`a\..\evil`, "x", 0o644}}},
		{name: "symlink", entries: []zipEntry{skillMD, {"link", "/etc/passwd", fs.ModeSymlink | 0o777}}},
		{name: "two top-level directories", entries: []zipEntry{{"a/SKILL.md", goodSkill, 0o644}, {"b/x", "x", 0o644}}},
		{name: "no SKILL.md", entries: []zipEntry{{"a/readme.md", "x", 0o644}}},
		{name: "too many entries", entries: append(many, zipEntry{"one-more", "", 0o644})},
		{name: "too deep", entries: []zipEntry{skillMD, {"a/b/c/d/e/f/g.txt", "x", 0o644}}},
		{name: "deep enough", ok: true, entries: []zipEntry{skillMD, {"a/b/c/d/e/g.txt", "x", 0o644}}},
		{name: "expands past the limit", entries: []zipEntry{skillMD, {"big.bin", strings.Repeat("\x00", MaxExpandedBytes), 0o644}}},
		{name: "SKILL.md over 64 KiB", entries: []zipEntry{{"SKILL.md", goodSkill + strings.Repeat("x", MaxSkillFileBytes), 0o644}}},
		{name: "duplicate path", entries: []zipEntry{skillMD, skillMD}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := makeZip(t, tt.entries...)
			_, err := ReadZip(bytes.NewReader(data), int64(len(data)))
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok = %v", err, tt.ok)
			}
		})
	}

	t.Run("archive over 5 MiB", func(t *testing.T) {
		if _, err := ReadZip(bytes.NewReader(nil), MaxZipBytes+1); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("not a zip", func(t *testing.T) {
		if _, err := ReadZip(strings.NewReader("hello"), 5); err == nil {
			t.Fatal("accepted")
		}
	})
}
