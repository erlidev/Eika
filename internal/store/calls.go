package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// ModelCall is the record of one model call a run made: what it sent besides
// the messages, what the runtime expected it to cost, and what the endpoint
// measured. The messages are the session's path down to EntryID, which the
// session already stores. The JSON documents are internal/agent's shapes; the
// store keeps them as they are given.
//
// Every count is nil when it is not known, which is not the same as 0.
type ModelCall struct {
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

	// PromptTokens, CompletionTokens, TotalTokens, and CachedTokens are what
	// the endpoint measured for the call: the prompt (cached part included),
	// everything generated (reasoning included), the total it stated, and the
	// part of the prompt it served from its cache.
	PromptTokens     *int
	CompletionTokens *int
	TotalTokens      *int
	CachedTokens     *int
	// PrefilledTokens is the part of the prompt the endpoint processed.
	PrefilledTokens *int
	// ReasoningTokens is the part of the completion that was reasoning.
	ReasoningTokens *int
	// ReasoningSource says where ReasoningTokens came from: "reported" is the
	// endpoint's own count and "tokenised" a count of the reasoning text by
	// the endpoint's tokenizer, which approximates what the model generated.
	// It is empty exactly when ReasoningTokens is nil.
	ReasoningSource string

	// PredictedPromptTokens and Prediction are what the token counter
	// expected the prompt to be before the call and how it knew: exact,
	// anchored or estimated. Drift is the prediction less the measured prompt,
	// nil unless both exist.
	PredictedPromptTokens *int
	Prediction            string
	Drift                 *int

	// ContextWindow is the window the call was judged against, nil when the
	// model has none configured.
	ContextWindow *int
	// Endpoint is the provider's name when the call was made.
	Endpoint string
	// QueueMS is the time the call waited for a lease on its pool, Slot the
	// queue's slot number of the lease that served it, Class the model class
	// the run was resolved from and Priority the priority it asked for. They
	// are nil or empty for a call made without the queue, or whose source
	// does not exist yet. Agent is filled by the agents item.
	QueueMS  *int
	Slot     *int
	Class    string
	Priority string
	Agent    string
	// PrefillMS and DecodeMS are how long the endpoint took to read the
	// prompt and to generate, nil when nothing measured them.
	PrefillMS *int
	DecodeMS  *int

	CreatedAt time.Time
}

// modelCallFields is the column list in the order scanModelCall reads them,
// without the large documents.
const modelCallFields = `parameters, message_tokens, prompt_tokens, completion_tokens, total_tokens,
	cached_tokens, prefilled_tokens, reasoning_tokens, reasoning_source, predicted_prompt_tokens,
	prediction, drift, context_window, endpoint, queue_ms, slot, class, priority, agent, prefill_ms,
	decode_ms, created_at`

// modelCallSummary is the column list a listing selects, in the order
// scanModelCall reads them: every column but the large documents, which read
// back as NULL.
const modelCallSummary = `id, session_id, run_id, entry_id, model_id, model, NULL::jsonb, NULL::jsonb, ` + modelCallFields

// modelCallColumns is the column list that reads a whole record.
const modelCallColumns = `id, session_id, run_id, entry_id, model_id, model, sections, tools, ` + modelCallFields

// RecordModelCall stores the record of one model call and returns it as
// stored. An empty ID gets a fresh one. A run or an entry that is not the
// session's is ErrNotFound; a model that no longer exists is recorded by
// name alone.
func (s *Store) RecordModelCall(ctx context.Context, c ModelCall) (ModelCall, error) {
	if c.ID == "" {
		c.ID = NewID()
	}
	// The checks are part of the insert, so that no record can name a run
	// or an entry of another session.
	const q = `INSERT INTO model_calls (id, session_id, run_id, entry_id, model_id, model, sections, tools,
			parameters, message_tokens, prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			prefilled_tokens, reasoning_tokens, reasoning_source, predicted_prompt_tokens, prediction, drift,
			context_window, endpoint, queue_ms, slot, class, priority, agent, prefill_ms, decode_ms)
		SELECT $1, $2, $3, $4, (SELECT id FROM models WHERE id = $5), $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29
		WHERE EXISTS (SELECT 1 FROM runs WHERE id = $3 AND session_id = $2)
			AND ($4::text IS NULL OR EXISTS (
				SELECT 1 FROM session_entries WHERE id = $4 AND session_id = $2))
		RETURNING ` + modelCallColumns
	out, err := scanModelCall(s.pool.QueryRow(ctx, q, c.ID, c.SessionID, c.RunID, nullable(c.EntryID),
		c.ModelID, c.Model, jsonArray(c.Sections), jsonArray(c.Tools), jsonObject(c.Parameters),
		c.MessageTokens, c.PromptTokens, c.CompletionTokens, c.TotalTokens, c.CachedTokens,
		c.PrefilledTokens, c.ReasoningTokens, nullable(c.ReasoningSource), c.PredictedPromptTokens,
		nullable(c.Prediction), c.Drift, c.ContextWindow, nullable(c.Endpoint), c.QueueMS, c.Slot,
		nullable(c.Class), nullable(c.Priority), nullable(c.Agent), c.PrefillMS, c.DecodeMS))
	if err != nil {
		return ModelCall{}, wrap("record model call of session "+c.SessionID, err)
	}
	return out, nil
}

// ModelCalls returns the records of one session's model calls, oldest
// first, without their sections and tools.
func (s *Store) ModelCalls(ctx context.Context, sessionID string) ([]ModelCall, error) {
	const q = `SELECT ` + modelCallSummary + ` FROM model_calls
		WHERE session_id = $1 ORDER BY created_at, id`
	op := "list model calls of session " + sessionID
	rows, err := s.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, wrap(op, err)
	}
	defer rows.Close()
	var out []ModelCall
	for rows.Next() {
		c, err := scanModelCall(rows)
		if err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(op, err)
	}
	return out, nil
}

// ModelCall returns one whole record of a session's model call. A record of
// another session is ErrNotFound.
func (s *Store) ModelCall(ctx context.Context, sessionID, id string) (ModelCall, error) {
	const q = `SELECT ` + modelCallColumns + ` FROM model_calls WHERE id = $1 AND session_id = $2`
	c, err := scanModelCall(s.pool.QueryRow(ctx, q, id, sessionID))
	if err != nil {
		return ModelCall{}, wrap("read model call "+id+" of session "+sessionID, err)
	}
	return c, nil
}

// The calls PrefillRate reads: the newest few that read a prompt long enough
// for the time to be the reading and not the call's fixed cost.
const (
	prefillRateCalls     = 20
	prefillRateMinTokens = 500
)

// PrefillRate returns the speed, in prompt tokens a second, the endpoint named
// read prompts at in its newest recorded calls: the tokens it processed over
// the time it stated for processing them. ok is false when no call states
// both, and the caller then has no measurement.
func (s *Store) PrefillRate(ctx context.Context, endpoint string) (tokensPerSecond int, ok bool, err error) {
	const q = `SELECT COALESCE(SUM(prefilled_tokens), 0), COALESCE(SUM(prefill_ms), 0) FROM (
			SELECT prefilled_tokens, prefill_ms FROM model_calls
			WHERE endpoint = $1 AND prefill_ms > 0 AND prefilled_tokens >= $2
			ORDER BY created_at DESC, id DESC LIMIT $3) newest`
	var tokens, ms int64
	if err := s.pool.QueryRow(ctx, q, endpoint, prefillRateMinTokens, prefillRateCalls).Scan(&tokens, &ms); err != nil {
		return 0, false, wrap("read the prefill rate of endpoint "+endpoint, err)
	}
	if tokens <= 0 || ms <= 0 {
		return 0, false, nil
	}
	return int(tokens * 1000 / ms), true, nil
}

// jsonArray is the value a jsonb array column is written from: nil or empty
// JSON is the empty array.
func jsonArray(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`[]`)
	}
	return raw
}

// scanModelCall reads one model call row.
func scanModelCall(row pgx.Row) (ModelCall, error) {
	var (
		c                                                               ModelCall
		entry, model                                                    *string
		sections, tools, parameters                                     []byte
		reasoningSource, prediction, endpoint, class, priority, agentID *string
	)
	err := row.Scan(&c.ID, &c.SessionID, &c.RunID, &entry, &model, &c.Model, &sections, &tools,
		&parameters, &c.MessageTokens, &c.PromptTokens, &c.CompletionTokens, &c.TotalTokens,
		&c.CachedTokens, &c.PrefilledTokens, &c.ReasoningTokens, &reasoningSource,
		&c.PredictedPromptTokens, &prediction, &c.Drift, &c.ContextWindow, &endpoint,
		&c.QueueMS, &c.Slot, &class, &priority, &agentID, &c.PrefillMS, &c.DecodeMS, &c.CreatedAt)
	if err != nil {
		return ModelCall{}, err
	}
	c.EntryID = text(entry)
	c.ModelID = text(model)
	c.ReasoningSource = text(reasoningSource)
	c.Prediction = text(prediction)
	c.Endpoint = text(endpoint)
	c.Class = text(class)
	c.Priority = text(priority)
	c.Agent = text(agentID)
	if sections != nil {
		c.Sections = json.RawMessage(sections)
	}
	if tools != nil {
		c.Tools = json.RawMessage(tools)
	}
	c.Parameters = json.RawMessage(parameters)
	c.CreatedAt = c.CreatedAt.UTC()
	return c, nil
}
