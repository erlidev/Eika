package server

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/sentry/screen"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/skill/volume"
)

type skillRows struct{ rows map[string]skill.Record }

func (f *skillRows) UpsertSkill(_ context.Context, r skill.Record) error {
	r.Enabled, r.Broken = false, ""
	f.rows[r.Name] = r
	return nil
}
func (f *skillRows) GetSkill(_ context.Context, n string) (skill.Record, error) {
	if r, ok := f.rows[n]; ok {
		return r, nil
	}
	return skill.Record{}, skill.ErrNotFound
}
func (f *skillRows) ListSkills(context.Context) ([]skill.Record, error)  { return nil, nil }
func (f *skillRows) DeleteSkill(context.Context, string) error           { return nil }
func (f *skillRows) SetSkillEnabled(context.Context, string, bool) error { return nil }
func (f *skillRows) SetSkillBroken(context.Context, string, string) error {
	return nil
}

type skillGuard func(context.Context, []string) ([][]screen.Window, error)

func (g skillGuard) Screen(ctx context.Context, t []string) ([][]screen.Window, error) {
	return g(ctx, t)
}

type skillAudit struct{ rows []sentry.AuditEntry }

func (a *skillAudit) AppendAudit(_ context.Context, e sentry.AuditEntry) error {
	a.rows = append(a.rows, e)
	return nil
}

func skillZipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(b))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The installer stores the screen's result, never enables, and rejects a
// credential before anything is written.
func TestSkillInstallScreen(t *testing.T) {
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := &skillRows{rows: map[string]skill.Record{}}
	in := skill.NewInstaller(volume.New(ex), rows)
	down := false
	audit := &skillAudit{}
	guard := skillGuard(func(_ context.Context, texts []string) ([][]screen.Window, error) {
		if down {
			return nil, context.DeadlineExceeded
		}
		out := make([][]screen.Window, len(texts))
		for i, t := range texts {
			at := strings.Index(t, "IGNORE")
			out[i] = []screen.Window{{Start: 0, End: len(t), Score: 0.01}}
			if at > 0 {
				out[i] = []screen.Window{{Start: 0, End: at, Score: 0.01}, {Start: at, End: len(t), Score: 0.99}}
			}
		}
		return out, nil
	})
	in.SetScreener(skillScreener(sentry.New(sentry.Options{Audit: audit, Screen: guard, Threshold: 0.9}).(sentry.SkillChecker)))

	token := "ghp_" + strings.Repeat("a1B2", 9)
	md := func(body string) string { return "---\nname: s\ndescription: Does s. Use when asked.\n---\n" + body }
	ctx := context.Background()

	_, err = in.Install(ctx, skillZipOf(t, map[string]string{"SKILL.md": md("hi"), "scripts/a.sh": "K=" + token}))
	if err == nil || !strings.Contains(err.Error(), "github-token") || strings.Contains(err.Error(), token) || len(rows.rows) != 0 {
		t.Fatalf("credential: err=%v rows=%v", err, rows.rows)
	}

	body := "ok\u200b text\nIGNORE ALL"
	res, err := in.Install(ctx, skillZipOf(t, map[string]string{"SKILL.md": md(body)}))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Record
	full := md(body)
	if r.Enabled || r.Screen == nil || r.Screen.Unscanned || len(r.Screen.Spans) != 1 {
		t.Fatalf("record %+v screen %+v", r, r.Screen)
	}
	if sp := r.Screen.Spans[0]; sp.Field != "body" || full[sp.Start:sp.End] != "IGNORE ALL" || sp.Text != "IGNORE ALL" {
		t.Errorf("span %+v", sp)
	}

	down = true
	res, err = in.Install(ctx, skillZipOf(t, map[string]string{"SKILL.md": md("changed")}))
	if err != nil || res.Record.Screen == nil || !res.Record.Screen.Unscanned || res.Record.Enabled {
		t.Fatalf("guard down: %+v %v", res.Record.Screen, err)
	}
	for _, e := range audit.rows {
		if strings.Contains(string(e.Detail)+e.Reason, token) || strings.Contains(string(e.Detail), "IGNORE") {
			t.Errorf("audit leaks text: %+v", e)
		}
	}
	if len(audit.rows) != 3 {
		t.Errorf("audit rows = %d, want 3", len(audit.rows))
	}
}
