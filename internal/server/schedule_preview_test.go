package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/erlidev/eika/internal/server"
)

func TestSchedulePreview(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})
	rec := requestWith(t, s, http.MethodPost, "/api/routines/preview", map[string]any{"schedule_kind": "cron", "schedule_expr": "0 8 * * *", "timezone": "UTC"}, "Bearer "+testToken)
	var got struct {
		Words string   `json:"schedule_words"`
		Next  []string `json:"next_five"`
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Words == "" || len(got.Next) != 5 {
		t.Fatalf("preview = %+v", got)
	}
	rec = requestWith(t, s, http.MethodPost, "/api/routines/preview", map[string]any{"schedule_kind": "cron", "schedule_expr": "bad", "timezone": "UTC"}, "Bearer "+testToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid schedule: status = %d", rec.Code)
	}
}
