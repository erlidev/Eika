package live

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScoreLogForwardsAndRemembersMax(t *testing.T) {
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"windows":[{"start":0,"end":3,"score":0.2}]},{"windows":[{"start":0,"end":3,"score":0.7}]}]}`))
	}))
	defer guard.Close()
	l, err := NewScoreLog(guard.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(l)
	defer front.Close()
	resp, err := http.Post(front.URL+"/screen", "application/json", strings.NewReader(`{"texts":["a","b"]}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	_ = resp.Body.Close()
	if m, n := l.Take(); m != 0.7 || n != 2 {
		t.Fatalf("max=%v texts=%d", m, n)
	}
	if m, n := l.Take(); m != 0 || n != 0 {
		t.Fatalf("not reset: %v %d", m, n)
	}
}

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
