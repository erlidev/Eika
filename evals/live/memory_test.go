package live

import "testing"

func TestPreambleMemoryAndSectionNames(t *testing.T) {
	text, err := PreambleMemory([]byte(`{"text":"p","memory":{"revision":"r","text":"## Memory\nIndex (x):\n- notes-format: bullets\n"}}`))
	if err != nil || text == "" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if named, indexed := SectionNames(text, "notes-format"); !named || !indexed {
		t.Fatalf("named=%v indexed=%v", named, indexed)
	}
	if named, _ := SectionNames(text, "notes"); named {
		t.Fatal("a name prefix matched")
	}
	if text, err := PreambleMemory([]byte(`{"text":"p"}`)); err != nil || text != "" {
		t.Fatalf("no section: %q %v", text, err)
	}
	if _, err := PreambleMemory([]byte(`{`)); err == nil {
		t.Fatal("bad payload accepted")
	}
}
