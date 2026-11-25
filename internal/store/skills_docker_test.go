//go:build docker

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/skill"
)

// Upgrading a populated database adds the skills table beside existing data,
// and an upsert over a row (a changed reinstall) returns it to disabled and
// sound while a broken mark survives until then.
func TestUpgradeAddsSkillsWithoutLosingData(t *testing.T) {
	ctx := t.Context()
	st, conn := upgradeFrom(t, "0033_skills_computer", `INSERT INTO blocked_hosts (host) VALUES ('keep.example')`)
	var host string
	if err := conn.QueryRow(ctx, `SELECT host FROM blocked_hosts`).Scan(&host); err != nil || host != "keep.example" {
		t.Fatalf("blocked host: %q %v", host, err)
	}
	if _, err := st.GetSkill(ctx, "pdf-forms"); !errors.Is(err, skill.ErrNotFound) {
		t.Fatalf("missing skill err = %v", err)
	}
	rec := skill.Record{
		Skill:       skill.Skill{Name: "pdf-forms", Description: "Fill forms.", Body: "v1", Metadata: map[string]string{"a": "b"}, Warnings: []string{"w"}},
		SHA:         "sha1",
		InstalledAt: time.Now().UTC().Truncate(time.Microsecond),
		Files:       []skill.FileInfo{{Path: "SKILL.md", Size: 2}},
		Screen:      &skill.ScreenResult{Unscanned: true},
	}
	if err := st.UpsertSkill(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSkill(ctx, "pdf-forms")
	if err != nil || got.Enabled || got.Broken != "" || got.SHA != "sha1" || got.Metadata["a"] != "b" ||
		len(got.Files) != 1 || got.Screen == nil || !got.Screen.Unscanned || !got.InstalledAt.Equal(rec.InstalledAt) {
		t.Fatalf("row = %+v, %v", got, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE skills SET enabled = true`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSkillBroken(ctx, "pdf-forms", skill.BrokenChanged); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.GetSkill(ctx, "pdf-forms"); got.Loadable() || !got.Enabled {
		t.Fatalf("broken row = %+v, want enabled but not loadable", got)
	}
	rec.SHA, rec.Body = "sha2", "v2"
	if err := st.UpsertSkill(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.GetSkill(ctx, "pdf-forms"); got.Enabled || got.Broken != "" || got.Body != "v2" {
		t.Fatalf("after reinstall = %+v, want disabled and sound", got)
	}
	if list, err := st.ListSkills(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := st.DeleteSkill(ctx, "pdf-forms"); err != nil {
		t.Fatal(err)
	}
}
