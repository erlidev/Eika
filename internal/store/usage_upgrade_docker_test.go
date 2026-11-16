//go:build docker

package store_test

import (
	"testing"
)

// A database at 0004 stored 0 for every count an endpoint did not state. The
// upgrade makes those NULL, since no call has an empty prompt, keeps every
// count that was stated, and leaves a stored cached count to be added.
func TestUpgradeFrom0004ReadsCallsThatStatedNothingAsNull(t *testing.T) {
	st, _ := upgradeFrom(t, "0004_provider_probe",
		`INSERT INTO sessions (id, title) VALUES ('s', 's')`,
		`INSERT INTO runs (id, session_id, state) VALUES ('r', 's', 'done')`,
		`INSERT INTO model_requests (id, session_id, run_id, model, input_tokens, output_tokens, total_tokens) VALUES ('stated', 's', 'r', 'm', 90, 100, 190)`,
		`INSERT INTO model_requests (id, session_id, run_id, model, input_tokens, output_tokens, total_tokens) VALUES ('zero-output', 's', 'r', 'm', 90, 0, 90)`,
		`INSERT INTO model_requests (id, session_id, run_id, model) VALUES ('silent', 's', 'r', 'm')`,
	)

	type counts struct{ in, out, total, cached int }
	want := map[string]counts{
		"stated":      {90, 100, 190, -1},
		"zero-output": {90, 0, 90, -1},
		"silent":      {-1, -1, -1, -1},
	}
	records, err := st.ModelCalls(t.Context(), "s")
	if err != nil || len(records) != len(want) {
		t.Fatalf("ModelCalls = %+v, %v; want every record kept", records, err)
	}
	for _, r := range records {
		got := counts{stated(r.PromptTokens), stated(r.CompletionTokens), stated(r.TotalTokens), stated(r.CachedTokens)}
		if got != want[r.ID] {
			t.Errorf("%s: in/out/total/cached = %+v, want %+v (-1 is NULL)", r.ID, got, want[r.ID])
		}
	}
}
