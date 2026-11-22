//go:build docker

package server_test

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/skill/volume"
)

func skillZipBytes(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("SKILL.md")
	_, _ = w.Write([]byte("---\nname: " + name + "\ndescription: Totals invoices. Use when asked.\n---\n" + body))
	w, _ = zw.Create("scripts/totals.py")
	_, _ = w.Write([]byte("print(1)\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// uploadSkill posts a multipart body with the bearer token and checks the
// answer against the contract.
func uploadSkill(t *testing.T, a *api, parts map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for field, data := range parts {
		w, _ := mw.CreateFormFile(field, "skill.zip")
		_, _ = w.Write(data)
	}
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/skills", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	a.Server.Handler().ServeHTTP(rec, r)
	checkContract(t, r, rec)
	return rec
}

func TestSkillsAPI(t *testing.T) {
	a := newAPI(t)
	if rec := request(t, a.Server, "GET", "/api/skills", nil); rec.Code != 503 {
		t.Fatalf("list before the installer is up = %d, want 503", rec.Code)
	}
	dir := t.TempDir()
	ex, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.SetSkillInstaller(skill.NewInstaller(volume.New(ex), a.store))
	sub := a.deps.Bus.Subscribe(event.TopicSkills)
	defer sub.Close()
	changes := func() string {
		e := <-sub.Events()
		var p event.SkillsChanged
		if e.Type != event.TypeSkillsChanged || e.DecodePayload(&p) != nil {
			t.Fatalf("event = %+v", e)
		}
		return p.Name + " " + p.Change
	}

	type skillWire struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Body    string `json:"body"`
		Files   []struct {
			Path string `json:"path"`
		} `json:"files"`
		Screen *struct {
			Unscanned bool `json:"unscanned"`
		} `json:"screen"`
	}
	type installed struct {
		Skill     skillWire `json:"skill"`
		Unchanged bool      `json:"unchanged"`
	}
	got := decodeBody[installed](t, uploadSkill(t, a, map[string][]byte{"file": skillZipBytes(t, "invoice-totals", "Run the script.")}), 201)
	if got.Skill.Name != "invoice-totals" || got.Skill.Enabled || got.Unchanged || len(got.Skill.Files) != 2 || got.Skill.Screen == nil {
		t.Fatalf("install = %+v, want a new disabled skill with two files and a screen", got)
	}
	if c := changes(); c != "invoice-totals installed" {
		t.Fatalf("event = %q", c)
	}
	if again := decodeBody[installed](t, uploadSkill(t, a, map[string][]byte{"file": skillZipBytes(t, "invoice-totals", "Run the script.")}), 201); !again.Unchanged {
		t.Fatalf("identical reinstall = %+v, want unchanged", again)
	}
	if list := decodeBody[struct {
		Skills []struct{ Name string } `json:"skills"`
	}](t, request(t, a.Server, "GET", "/api/skills", nil), 200); len(list.Skills) != 1 {
		t.Fatalf("list = %+v", list)
	}
	if read := decodeBody[skillWire](t, request(t, a.Server, "GET", "/api/skills/invoice-totals", nil), 200); read.Body != "Run the script." {
		t.Fatalf("read = %+v", read)
	}
	if rec := request(t, a.Server, "GET", "/api/skills/Bad..Name", nil); rec.Code != 404 {
		t.Fatalf("invalid name = %d", rec.Code)
	}
	// Without an exact token count the catalog cannot be judged: refused,
	// nothing changes and nothing is published.
	if rec := request(t, a.Server, "POST", "/api/skills/invoice-totals/enable", nil); rec.Code != 503 {
		t.Fatalf("enable without exact count = %d: %s", rec.Code, rec.Body.String())
	}
	if read := decodeBody[skillWire](t, request(t, a.Server, "GET", "/api/skills/invoice-totals", nil), 200); read.Enabled {
		t.Fatal("a refused enable enabled the skill")
	}
	if rec := request(t, a.Server, "POST", "/api/skills/missing/disable", nil); rec.Code != 404 {
		t.Fatalf("disable unknown = %d", rec.Code)
	}
	if rec := request(t, a.Server, "DELETE", "/api/skills/invoice-totals", nil); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if c := changes(); c != "invoice-totals deleted" {
		t.Fatalf("event = %q", c)
	}
	if rec := request(t, a.Server, "GET", "/api/skills/invoice-totals", nil); rec.Code != 404 {
		t.Fatalf("read after delete = %d", rec.Code)
	}
}

func TestSkillUploadIsBoundedAndChecked(t *testing.T) {
	a := newAPI(t)
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.SetSkillInstaller(skill.NewInstaller(volume.New(ex), a.store))
	for name, c := range map[string]struct {
		parts map[string][]byte
		want  int
	}{
		"not a zip":     {map[string][]byte{"file": []byte("hello")}, http.StatusBadRequest},
		"wrong field":   {map[string][]byte{"zip": skillZipBytes(t, "x-y", "b")}, http.StatusBadRequest},
		"over the size": {map[string][]byte{"file": bytes.Repeat([]byte("a"), skill.MaxZipBytes+1)}, http.StatusRequestEntityTooLarge},
	} {
		if rec := uploadSkill(t, a, c.parts); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d: %s", name, rec.Code, c.want, rec.Body.String())
		}
	}
	if rec := request(t, a.Server, "POST", "/api/skills", map[string]any{"file": "x"}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "multipart") {
		t.Errorf("JSON body = %d: %s", rec.Code, rec.Body.String())
	}
}
