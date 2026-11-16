//go:build docker

package store_test

import (
	"testing"
)

// A database at 0005 holds its model calls in model_requests, with the prompt
// and completion counts under their old names. The upgrade renames the table
// and the columns without touching a row.
func TestUpgradeFrom0005RenamesModelRequestsToModelCalls(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0005_model_request_usage",
		`INSERT INTO sessions (id, title) VALUES ('s', 's')`,
		`INSERT INTO runs (id, session_id, state) VALUES ('r', 's', 'done')`,
		`INSERT INTO model_requests (id, session_id, run_id, model, input_tokens, output_tokens, total_tokens, cached_tokens) VALUES ('stated', 's', 'r', 'm', 90, 100, 190, 86)`,
		`INSERT INTO model_requests (id, session_id, run_id, model) VALUES ('silent', 's', 'r', 'm')`,
	)

	calls, err := st.ModelCalls(ctx, "s")
	if err != nil || len(calls) != 2 {
		t.Fatalf("ModelCalls = %+v, %v; want both rows kept", calls, err)
	}
	for _, c := range calls {
		switch c.ID {
		case "stated":
			if stated(c.PromptTokens) != 90 || stated(c.CompletionTokens) != 100 || stated(c.TotalTokens) != 190 || stated(c.CachedTokens) != 86 {
				t.Errorf("stated call = %+v, want its counts under their new names", c)
			}
		case "silent":
			if c.PromptTokens != nil || c.CompletionTokens != nil || c.CachedTokens != nil {
				t.Errorf("silent call = %+v, want its counts still NULL", c)
			}
		}
	}

}
