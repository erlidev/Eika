package server

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestProjectRequestsRejectLegacyFields(t *testing.T) {
	for _, field := range []string{"kind", "host_path", "remote_url", "default_branch", "instructions", "about_me", "rules"} {
		t.Run(field, func(t *testing.T) {
			create := fmt.Sprintf(`{"name":"Taxes","slug":"taxes",%q:"legacy"}`, field)
			if _, err := decodeJSON[createProjectRequest](httptest.NewRequest("POST", "/api/projects", strings.NewReader(create))); err == nil {
				t.Fatal("create accepted legacy field")
			}
			update := fmt.Sprintf(`{%q:"legacy"}`, field)
			if _, err := decodeJSON[store.ProjectUpdate](httptest.NewRequest("PATCH", "/api/projects/id", strings.NewReader(update))); err == nil {
				t.Fatal("update accepted legacy field")
			}
		})
	}
}
