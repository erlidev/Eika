package server

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The API reference documents each route as a heading, "### `GET /api/x`",
// or as a row of a route table, "| GET | `/healthz` | ...".
var (
	routeHeading = regexp.MustCompile("^### `([A-Z]+ /[^`]*)`")
	routeRow     = regexp.MustCompile("^\\| ([A-Z]+) \\| `(/[^`]*)` \\|")
	eventHeading = regexp.MustCompile("^### `([a-z_]+\\.[a-z_.]+)`")
)

// TestHTTPDocsMatchTheRoutes keeps docs/api/http.md in step with routes.go:
// every route with a method is documented, and nothing else is.
func TestHTTPDocsMatchTheRoutes(t *testing.T) {
	var registered []string
	for _, call := range handleCalls(t) {
		registered = append(registered, call.pattern)
	}
	var documented []string
	for _, line := range docLines(t, "../../docs/api/http.md") {
		if m := routeHeading.FindStringSubmatch(line); m != nil {
			documented = append(documented, m[1])
		} else if m := routeRow.FindStringSubmatch(line); m != nil {
			documented = append(documented, m[1]+" "+m[2])
		}
	}
	compareSets(t, registered, documented, "docs/api/http.md")
}

// TestEventDocsMatchTheEvents keeps docs/api/events.md in step with the event
// types internal/event declares. The transport's own messages, documented
// under "Transport", are not events.
func TestEventDocsMatchTheEvents(t *testing.T) {
	declared := constants(t, "../event/event.go", "Type")
	var documented []string
	section := ""
	for _, line := range docLines(t, "../../docs/api/events.md") {
		if strings.HasPrefix(line, "## ") {
			section = line
		}
		if m := eventHeading.FindStringSubmatch(line); m != nil && section != "## Transport" {
			documented = append(documented, m[1])
		}
	}
	compareSets(t, declared, documented, "docs/api/events.md")
}

// docLines reads a document's lines.
func docLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(string(data), "\n")
}

// compareSets reports what the code has that the document lacks, what the
// document has that the code lacks, and anything the document lists twice.
func compareSets(t *testing.T, code, documented []string, doc string) {
	t.Helper()
	sort.Strings(documented)
	for i := 1; i < len(documented); i++ {
		if documented[i] == documented[i-1] {
			t.Errorf("%s documents %s twice", doc, documented[i])
		}
	}
	for _, x := range code {
		if !slices.Contains(documented, x) {
			t.Errorf("%s does not document %s", doc, x)
		}
	}
	for _, x := range documented {
		if !slices.Contains(code, x) {
			t.Errorf("%s documents %s, which the code does not have", doc, x)
		}
	}
}
