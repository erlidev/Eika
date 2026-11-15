package server

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestPreambleIsStableAndListsOnlyActiveProjects(t *testing.T) {
	d := preambleData{global: store.GlobalInstructions{AboutMe: "About", Rules: "Rules"}, projectInstructions: "Project rules", home: true, zone: "UTC", locale: "en-US", projects: []store.Project{
		{Slug: "z", ProjectFields: store.ProjectFields{Name: "Z", Status: "active", Description: "Last"}},
		{Slug: "hidden", ProjectFields: store.ProjectFields{Name: "Hidden", Status: "done"}},
		{Slug: "a", ProjectFields: store.ProjectFields{Name: "A", Status: "active", Description: "First"}},
	}}
	got := renderPreamble(d)
	want := "About me\nAbout\n\nRules\nRules\n\nProject instructions\nProject rules\n\nActive projects\n- A (a): First\n- Z (z): Last\n\nDelegable agents\n- researcher: Reads the web and reports findings.\n- worker: Does work on a computer and reports results.\n\nTime zone\nUTC\n\nLocale\nen-US"
	if got != want {
		t.Fatalf("preamble = %q, want %q", got, want)
	}
	if strings.Contains(got, "Hidden") {
		t.Fatal("inactive project included")
	}
}

func TestPreambleIncludesProjectAndBrief(t *testing.T) {
	p := store.Project{Slug: "work", ProjectFields: store.ProjectFields{Name: "Work", Description: "A task", Directory: "/work"}}
	got := renderPreamble(preambleData{project: &p, brief: "Do it", zone: "UTC", locale: "unspecified"})
	for _, want := range []string{"Work (work): A task", "Directory: /work", "Brief\nDo it"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestPreambleSettingValidation(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{"time_zone", `"UTC"`, true}, {"time_zone", `"bad-zone"`, false},
		{"time_zone", `12`, false}, {"time_zone", `null`, true},
		{"locale", `"en-US"`, true}, {"locale", `""`, false}, {"locale", `"en_US"`, false},
	} {
		err := s.validateSetting(t.Context(), tc.key, []byte(tc.value))
		if (err == nil) != tc.valid {
			t.Errorf("%s=%s: %v", tc.key, tc.value, err)
		}
	}
}
