package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/tool"
)

type skillsFake struct{ recs []skill.Record }

func (f skillsFake) GetSkill(_ context.Context, name string) (skill.Record, error) {
	for _, r := range f.recs {
		if r.Name == name {
			return r, nil
		}
	}
	return skill.Record{}, skill.ErrNotFound
}
func (f skillsFake) ListSkills(context.Context) ([]skill.Record, error) { return f.recs, nil }

func skillRec(name, desc string, enabled bool, broken string) skill.Record {
	return skill.Record{
		Skill:   skill.Skill{Name: name, Description: desc, Body: "Do the " + name + " thing."},
		Enabled: enabled, Broken: broken,
		Files: []skill.FileInfo{{Path: "SKILL.md", Size: 9}, {Path: "scripts/run.py", Size: 42}},
	}
}

func TestSkillUse(t *testing.T) {
	recs := []skill.Record{
		skillRec("alpha", "Fill PDF forms.", true, ""),
		skillRec("off", "Fill PDF forms, disabled.", false, ""),
		skillRec("torn", "Fill PDF forms, broken.", true, "directory missing"),
	}
	for i := 0; i < 12; i++ {
		recs = append(recs, skillRec("many-"+string(rune('a'+i)), "bulk match", true, ""))
	}
	r, err := Registry(Deps{Skills: skillsFake{recs}})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := r.Get("skill_use")
	call := func(args string) tool.Result {
		t.Helper()
		res, err := st.Call(context.Background(), tool.CallContext{}, []byte(args))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := call(`{"name":"alpha"}`)
	if res.IsError || !strings.Contains(res.Content, `<skill name="alpha" dir="/skills/alpha">`) ||
		!strings.Contains(res.Content, "Do the alpha thing.") || !strings.Contains(res.Content, "scripts/run.py") ||
		strings.Contains(res.Content, "SKILL.md") {
		t.Errorf("load = %+v", res)
	}
	var first string
	for _, n := range []string{"off", "torn", "ghost"} {
		res = call(`{"name":"` + n + `"}`)
		if !res.IsError || strings.Contains(res.Content, "disabled") || strings.Contains(res.Content, "broken") {
			t.Errorf("%s = %+v", n, res)
		}
		if first == "" {
			first = strings.ReplaceAll(res.Content, `"off"`, "X")
		} else if strings.ReplaceAll(strings.ReplaceAll(res.Content, `"torn"`, "X"), `"ghost"`, "X") != first {
			t.Errorf("reply differs by state: %q", res.Content)
		}
	}
	res = call(`{"query":"fill pdf"}`)
	if res.IsError || res.Content != "- alpha: Fill PDF forms." {
		t.Errorf("query = %+v", res)
	}
	if n := strings.Count(call(`{"query":"bulk"}`).Content, "\n") + 1; n != 10 {
		t.Errorf("matches = %d", n)
	}
	for _, bad := range []string{`{}`, `{"name":"a","query":"b"}`, `{"query":" "}`, `{"name":1}`, `{"other":"x"}`} {
		if !call(bad).IsError {
			t.Errorf("%s accepted", bad)
		}
	}
}
