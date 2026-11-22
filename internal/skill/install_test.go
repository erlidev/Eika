package skill_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/skill/volume"
)

var errInjected = errors.New("injected failure")

type fakeIndex struct {
	rows       map[string]skill.Record
	failUpsert bool
}

func (f *fakeIndex) UpsertSkill(_ context.Context, r skill.Record) error {
	if f.failUpsert {
		return errInjected
	}
	r.Enabled, r.Broken = false, ""
	f.rows[r.Name] = r
	return nil
}
func (f *fakeIndex) GetSkill(_ context.Context, name string) (skill.Record, error) {
	r, ok := f.rows[name]
	if !ok {
		return skill.Record{}, skill.ErrNotFound
	}
	return r, nil
}
func (f *fakeIndex) ListSkills(context.Context) ([]skill.Record, error) {
	var out []skill.Record
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeIndex) DeleteSkill(_ context.Context, name string) error {
	delete(f.rows, name)
	return nil
}
func (f *fakeIndex) SetSkillEnabled(_ context.Context, name string, enabled bool) error {
	r, ok := f.rows[name]
	if !ok {
		return skill.ErrNotFound
	}
	r.Enabled = enabled
	f.rows[name] = r
	return nil
}
func (f *fakeIndex) SetSkillBroken(_ context.Context, name, reason string) error {
	r := f.rows[name]
	r.Broken = reason
	f.rows[name] = r
	return nil
}

type env struct {
	dir string
	idx *fakeIndex
	in  *skill.Installer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	ex, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx := &fakeIndex{rows: map[string]skill.Record{}}
	return &env{dir: dir, idx: idx, in: skill.NewInstaller(volume.New(ex), idx)}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func skillZip(t *testing.T, name, body string) []byte {
	return zipOf(t, map[string]string{
		"SKILL.md":       "---\nname: " + name + "\ndescription: Does " + name + ". Use when asked.\n---\n" + body,
		"notes/guide.md": "guide " + body,
	})
}

func TestInstallCreatesDisabledRowAndUpgradeResetsIt(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	res, err := e.in.Install(ctx, skillZip(t, "pdf-forms", "v1"))
	if err != nil || res.Unchanged {
		t.Fatalf("install: %+v, %v", res, err)
	}
	r := e.idx.rows["pdf-forms"]
	if r.Enabled || r.Screen == nil || !r.Screen.Unscanned || len(r.Files) != 2 || r.Body != "v1" {
		t.Fatalf("row = %+v, want disabled, unscanned, two files", r)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "pdf-forms", "notes", "guide.md")); err != nil {
		t.Fatal(err)
	}

	// The user enables it; installing the same bytes changes nothing.
	r.Enabled = true
	e.idx.rows["pdf-forms"] = r
	if res, err = e.in.Install(ctx, skillZip(t, "pdf-forms", "v1")); err != nil || !res.Unchanged || !e.idx.rows["pdf-forms"].Enabled {
		t.Fatalf("identical reinstall: %+v, %v; want a no-op that stays enabled", res, err)
	}

	// A changed reinstall replaces directory and row and returns to disabled.
	if res, err = e.in.Install(ctx, skillZip(t, "pdf-forms", "v2")); err != nil || res.Unchanged {
		t.Fatalf("upgrade: %+v, %v", res, err)
	}
	r = e.idx.rows["pdf-forms"]
	if r.Enabled || r.Body != "v2" || r.SHA == "" {
		t.Fatalf("after upgrade: %+v", r)
	}
	if got, err := os.ReadFile(filepath.Join(e.dir, "pdf-forms", "notes", "guide.md")); err != nil || string(got) != "guide v2" {
		t.Fatalf("directory after upgrade: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".old", "pdf-forms")); !os.IsNotExist(err) {
		t.Errorf(".old left behind: %v", err)
	}
}

func TestFailureBetweenDirectoryAndRowIsRepairedByReconcile(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, err := e.in.Install(ctx, skillZip(t, "pdf-forms", "v1")); err != nil {
		t.Fatal(err)
	}
	e.idx.failUpsert = true
	if _, err := e.in.Install(ctx, skillZip(t, "pdf-forms", "v2")); !errors.Is(err, errInjected) {
		t.Fatalf("upgrade err = %v, want the injected failure", err)
	}
	if _, err := e.in.Install(ctx, skillZip(t, "new-one", "x")); !errors.Is(err, errInjected) {
		t.Fatalf("new install err = %v", err)
	}
	// The directories are ahead of the rows.
	rep, err := e.in.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Broken, []string{"pdf-forms"}) || !slices.Equal(rep.Unknown, []string{"new-one"}) {
		t.Fatalf("report = %+v, want pdf-forms broken and new-one unknown", rep)
	}
	if _, ok := e.idx.rows["new-one"]; ok {
		t.Fatal("an unknown directory was adopted")
	}
	if r := e.idx.rows["pdf-forms"]; r.Broken != skill.BrokenChanged || r.Loadable() {
		t.Fatalf("row = %+v, want broken and not loadable", r)
	}
	// Installing again repairs both.
	e.idx.failUpsert = false
	if _, err := e.in.Install(ctx, skillZip(t, "pdf-forms", "v2")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.in.Install(ctx, skillZip(t, "new-one", "x")); err != nil {
		t.Fatal(err)
	}
	if rep, err = e.in.Reconcile(ctx); err != nil || len(rep.Broken)+len(rep.Unknown)+len(rep.Repaired) != 0 || e.idx.rows["pdf-forms"].Broken != "" {
		t.Fatalf("after repair: %+v, %v", rep, err)
	}
}

func TestReconcileMarksMissingAndChangedAndNeverAdopts(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, n := range []string{"gone", "edited", "sound", "crashed"} {
		if _, err := e.in.Install(ctx, skillZip(t, n, n)); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.RemoveAll(filepath.Join(e.dir, "gone")))
	must(os.WriteFile(filepath.Join(e.dir, "edited", "SKILL.md"), []byte("tampered"), 0o644))
	// A crash inside Replace: the name is missing and the old tree is aside.
	must(os.MkdirAll(filepath.Join(e.dir, ".old"), 0o755))
	must(os.Rename(filepath.Join(e.dir, "crashed"), filepath.Join(e.dir, ".old", "crashed")))
	must(os.MkdirAll(filepath.Join(e.dir, "stranger"), 0o755))
	must(os.WriteFile(filepath.Join(e.dir, "stranger", "SKILL.md"), []byte("x"), 0o644))

	rep, err := e.in.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(rep.Broken, "crashed", "edited", "gone") {
		t.Fatalf("broken = %v", rep.Broken)
	}
	if !slices.Equal(rep.Unknown, []string{"stranger"}) {
		t.Fatalf("unknown = %v, want only stranger (.old is a working directory)", rep.Unknown)
	}
	if e.idx.rows["gone"].Broken != skill.BrokenMissing || e.idx.rows["crashed"].Broken != skill.BrokenMissing ||
		e.idx.rows["edited"].Broken != skill.BrokenChanged || e.idx.rows["sound"].Broken != "" {
		t.Fatalf("rows: %+v", e.idx.rows)
	}
	if _, ok := e.idx.rows["stranger"]; ok {
		t.Fatal("unknown directory adopted")
	}
	// A second reconcile has nothing new to report; a restored directory repairs.
	if rep, err = e.in.Reconcile(ctx); err != nil || len(rep.Broken) != 0 {
		t.Fatalf("second reconcile: %+v, %v", rep, err)
	}
	must(os.Rename(filepath.Join(e.dir, ".old", "crashed"), filepath.Join(e.dir, "crashed")))
	if rep, err = e.in.Reconcile(ctx); err != nil || !slices.Equal(rep.Repaired, []string{"crashed"}) {
		t.Fatalf("after restore: %+v, %v", rep, err)
	}
}

func sameSet(got []string, want ...string) bool {
	g := slices.Clone(got)
	slices.Sort(g)
	return slices.Equal(g, want)
}
