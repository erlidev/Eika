package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// ModelRequest is the record of one model call a run made: what it sent
// besides the messages, and what the endpoint measured. The messages are
// the session's path down to EntryID, which the session already stores.
// The JSON documents are internal/agent's shapes; the store keeps them as
// they are given.
type ModelRequest struct {
	ID        string
	SessionID string
	// RunID is the run that made the call.
	RunID string
	// EntryID is the session entry the call's conversation ended at, empty
	// for a call that sent no messages.
	EntryID string
	// ModelID is the model the call used, empty once that model is deleted;
	// Model is its name when the call was made, which stays.
	ModelID string
	Model   string
	// Sections are the system prompt's sections and Tools the tool schemas,
	// as JSON arrays. A listing leaves both nil.
	Sections json.RawMessage
	Tools    json.RawMessage
	// Parameters are the model and sampling parameters sent, as a JSON
	// object.
	Parameters json.RawMessage
	// MessageTokens is the estimated size of the messages sent.
	MessageTokens int
	// InputTokens, OutputTokens, and TotalTokens are what the endpoint
	// measured for the call, zero when it reported nothing.
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CreatedAt    time.Time
}

// modelRequestSummary is the column list a listing selects, in the order
// scanModelRequest reads them: every column but the large documents, which
// read back as NULL.
const modelRequestSummary = `id, session_id, run_id, entry_id, model_id, model, NULL::jsonb, NULL::jsonb,
	parameters, message_tokens, input_tokens, output_tokens, total_tokens, created_at`

// modelRequestColumns is the column list that reads a whole record.
const modelRequestColumns = `id, session_id, run_id, entry_id, model_id, model, sections, tools,
	parameters, message_tokens, input_tokens, output_tokens, total_tokens, created_at`

// RecordModelRequest stores the record of one model call and returns it as
// stored. An empty ID gets a fresh one. A run or an entry that is not the
// session's is ErrNotFound; a model that no longer exists is recorded by
// name alone.
func (s *Store) RecordModelRequest(ctx context.Context, r ModelRequest) (ModelRequest, error) {
	if r.ID == "" {
		r.ID = NewID()
	}
	// The checks are part of the insert, so that no record can name a run
	// or an entry of another session.
	const q = `INSERT INTO model_requests (id, session_id, run_id, entry_id, model_id, model, sections, tools,
			parameters, message_tokens, input_tokens, output_tokens, total_tokens)
		SELECT $1, $2, $3, $4, (SELECT id FROM models WHERE id = $5), $6, $7, $8, $9, $10, $11, $12, $13
		WHERE EXISTS (SELECT 1 FROM runs WHERE id = $3 AND session_id = $2)
			AND ($4::text IS NULL OR EXISTS (
				SELECT 1 FROM session_entries WHERE id = $4 AND session_id = $2))
		RETURNING ` + modelRequestColumns
	out, err := scanModelRequest(s.pool.QueryRow(ctx, q, r.ID, r.SessionID, r.RunID, nullable(r.EntryID),
		r.ModelID, r.Model, jsonArray(r.Sections), jsonArray(r.Tools), jsonObject(r.Parameters),
		r.MessageTokens, r.InputTokens, r.OutputTokens, r.TotalTokens))
	if err != nil {
		return ModelRequest{}, wrap("record model request of session "+r.SessionID, err)
	}
	return out, nil
}

// ModelRequests returns the records of one session's model calls, oldest
// first, without their sections and tools.
func (s *Store) ModelRequests(ctx context.Context, sessionID string) ([]ModelRequest, error) {
	const q = `SELECT ` + modelRequestSummary + ` FROM model_requests
		WHERE session_id = $1 ORDER BY created_at, id`
	op := "list model requests of session " + sessionID
	rows, err := s.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []ModelRequest
	for rows.Next() {
		r, err := scanModelRequest(rows)
		if err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(op, err)
	}
	return out, nil
}

// ModelRequest returns one whole record of a session's model call. A record
// of another session is ErrNotFound.
func (s *Store) ModelRequest(ctx context.Context, sessionID, id string) (ModelRequest, error) {
	const q = `SELECT ` + modelRequestColumns + ` FROM model_requests WHERE id = $1 AND session_id = $2`
	r, err := scanModelRequest(s.pool.QueryRow(ctx, q, id, sessionID))
	if err != nil {
		return ModelRequest{}, wrap("read model request "+id+" of session "+sessionID, err)
	}
	return r, nil
}

// jsonArray is the value a jsonb array column is written from: nil or empty
// JSON is the empty array.
func jsonArray(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`[]`)
	}
	return raw
}

// scanModelRequest reads one model request row.
func scanModelRequest(row pgx.Row) (ModelRequest, error) {
	var (
		r          ModelRequest
		entry      *string
		model      *string
		sections   []byte
		tools      []byte
		parameters []byte
	)
	err := row.Scan(&r.ID, &r.SessionID, &r.RunID, &entry, &model, &r.Model, &sections, &tools,
		&parameters, &r.MessageTokens, &r.InputTokens, &r.OutputTokens, &r.TotalTokens, &r.CreatedAt)
	if err != nil {
		return ModelRequest{}, err
	}
	r.EntryID = text(entry)
	r.ModelID = text(model)
	if sections != nil {
		r.Sections = json.RawMessage(sections)
	}
	if tools != nil {
		r.Tools = json.RawMessage(tools)
	}
	r.Parameters = json.RawMessage(parameters)
	r.CreatedAt = r.CreatedAt.UTC()
	return r, nil
}
