package agent

import "testing"

func TestPreambleAppearsInPreview(t *testing.T) {
	a := New(nil, nil, Options{})
	s := NewSession("id", "")
	s.Preamble = "Snapshot\n\nTime zone\nUTC\n\nLocale\nen"
	c, err := a.Preview(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sections) != 2 || c.Sections[1].Kind != SectionPreamble || c.Sections[1].Text != s.Preamble {
		t.Fatalf("sections = %#v", c.Sections)
	}
	if got := PreambleLocation(s.Preamble).String(); got != "UTC" {
		t.Fatalf("zone = %q", got)
	}
}
