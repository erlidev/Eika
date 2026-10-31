package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// modelCallBody is the record of one model call on the wire, without what it
// sent. A count is null when it is not known, which is not the same as 0.
type modelCallBody struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	// RunID is the run that made the call.
	RunID string `json:"run_id"`
	// EntryID is the session entry the call's conversation ended at.
	EntryID string `json:"entry_id,omitempty"`
	// ModelID is absent once the model is deleted; Model is its name when
	// the call was made.
	ModelID string `json:"model_id,omitempty"`
	Model   string `json:"model"`
	// MessageTokens is the estimated size of the messages sent.
	MessageTokens int `json:"message_tokens"`
	// PromptTokens, CompletionTokens, TotalTokens, and CachedTokens are what
	// the endpoint measured: the prompt (cached part included), everything
	// generated (reasoning included), the total it stated, and the part of the
	// prompt it served from its cache.
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
	TotalTokens      *int `json:"total_tokens"`
	CachedTokens     *int `json:"cached_tokens"`
	// PrefilledTokens is the part of the prompt the endpoint processed.
	PrefilledTokens *int `json:"prefilled_tokens"`
	// ReasoningTokens is the part of the completion that was reasoning, and
	// ReasoningSource where it came from: "reported" by the endpoint, or
	// "tokenised" from the reasoning text, which approximates it. The source
	// is absent exactly when the count is null.
	ReasoningTokens *int   `json:"reasoning_tokens"`
	ReasoningSource string `json:"reasoning_source,omitempty"`
	// PredictedPromptTokens is what the token counter expected the prompt to
	// be before the call, Prediction how it knew (exact, anchored or
	// estimated), and Drift the prediction less PromptTokens. All three are
	// null when nothing was predicted, and Drift when nothing was measured.
	// Any drift on an exact prediction is a bug.
	PredictedPromptTokens *int   `json:"predicted_prompt_tokens"`
	Prediction            string `json:"prediction,omitempty"`
	Drift                 *int   `json:"drift"`
	// ContextWindow is the window the call was judged against; null when the
	// model had none configured.
	ContextWindow *int `json:"context_window"`
	// Endpoint is the provider's name when the call was made.
	Endpoint string `json:"endpoint,omitempty"`
	// QueueMS, Slot, Class, Priority and Agent are null until the inference
	// queue and the agents fill them.
	QueueMS   *int      `json:"queue_ms"`
	Slot      *int      `json:"slot"`
	Class     string    `json:"class,omitempty"`
	Priority  string    `json:"priority,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// callsResponse is the body of GET /api/sessions/{id}/calls.
type callsResponse struct {
	SessionID string `json:"session_id"`
	// Calls are the session's recorded model calls, oldest first.
	Calls []modelCallBody `json:"calls"`
}

// calibrationBody is one measured model call beside the estimate of what it
// sent, so an estimate can be scaled to what the endpoint counts.
type calibrationBody struct {
	CallID string `json:"call_id"`
	// PromptTokens is what the endpoint measured the call's prompt as.
	PromptTokens int `json:"prompt_tokens"`
	// EstimatedTokens is the estimate of the same input: its sections, tool
	// schemas, and messages.
	EstimatedTokens int `json:"estimated_tokens"`
}

// contextResponse is the body of GET /api/sessions/{id}/context, the next
// model call of the session, and of GET /api/sessions/{id}/calls/{id}, a
// recorded one: the system prompt by section, the tool schemas, the
// messages, and the parameters, with the layer each parameter came from.
type contextResponse struct {
	agent.Context
	// Sources names the layer each parameter came from, keyed model,
	// thinking_switch, preserve_thinking, image_input, and
	// sampling.<parameter>.
	Sources map[string]string `json:"sources"`
	// DroppedEffort is a reasoning effort the configuration chose that the
	// model does not offer, which is not sent.
	DroppedEffort string `json:"dropped_effort,omitempty"`
	// Call is the record this is, absent for the next call.
	Call *modelCallBody `json:"call,omitempty"`
	// ContextWindow is the context window of the call's model, in tokens:
	// what the request has to fit in. Zero when the model is not known,
	// because none is configured or a recorded call's model is deleted.
	ContextWindow int `json:"context_window"`
	// Calibration is the measured call the estimates can be scaled to: the
	// recorded call itself, or the session's last measured one; absent when
	// there is none.
	Calibration *calibrationBody `json:"calibration,omitempty"`
}

// recordedParameters is what a record keeps as its parameters: those the
// call sent, and where each came from.
type recordedParameters struct {
	agent.Parameters
	Sources       map[string]string `json:"sources,omitempty"`
	DroppedEffort string            `json:"dropped_effort,omitempty"`
}

// callRecorder keeps a record of every model call a run makes in the
// model_calls table.
type callRecorder struct {
	store  *store.Store
	run    *activeRun
	config runConfig
	// endpoint is the name of the provider the run's model is on.
	endpoint string
}

// positive returns a pointer to n, or nil for a count that is zero or less,
// which a column that must be positive stores as NULL.
func positive(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// Record stores one model call. The conversation it sent ended at the
// session's head, which is the newest entry the run stored.
func (c callRecorder) Record(ctx context.Context, call agent.ModelCall) error {
	sess, err := c.store.Session(ctx, call.SessionID)
	if err != nil {
		return err
	}
	sections, err := json.Marshal(call.Context.Sections)
	if err != nil {
		return fmt.Errorf("encode sections: %w", err)
	}
	tools, err := json.Marshal(call.Context.Tools)
	if err != nil {
		return fmt.Errorf("encode tools: %w", err)
	}
	parameters, err := json.Marshal(recordedParameters{
		Parameters:    call.Context.Parameters,
		Sources:       c.config.parameterSources(),
		DroppedEffort: c.config.droppedEffort,
	})
	if err != nil {
		return fmt.Errorf("encode parameters: %w", err)
	}
	_, err = c.store.RecordModelCall(ctx, store.ModelCall{
		SessionID:     call.SessionID,
		RunID:         c.run.runID(),
		EntryID:       sess.HeadEntryID,
		ModelID:       c.config.model.ID,
		Model:         c.config.model.Name,
		Sections:      sections,
		Tools:         tools,
		Parameters:    parameters,
		MessageTokens: call.Context.MessageTokens,

		PromptTokens:     call.Usage.InputTokens.Ptr(),
		CompletionTokens: call.Usage.OutputTokens.Ptr(),
		TotalTokens:      call.Usage.TotalTokens.Ptr(),
		CachedTokens:     call.Usage.CachedTokens.Ptr(),
		PrefilledTokens:  call.Usage.Prefilled().Ptr(),
		ReasoningTokens:  call.Reasoning.Ptr(),
		ReasoningSource:  call.ReasoningSource,
		ContextWindow:    positive(call.ContextWindow),
		Endpoint:         c.endpoint,
	})
	return err
}

// handleSessionContext previews the next model call of a session: the
// request a run started now would send, assembled by the same code a run
// uses, with the run's new message still to be appended. model names the
// model the run would request, as a message does. The MCP tools are those
// the servers last listed, since a preview connects nothing.
func (s *Server) handleSessionContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.deps.Store.Session(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cfg, err := s.configure(ctx, sess, r.URL.Query().Get("model"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	opts := agent.Options{Logger: s.log}
	loaded, err := session.NewStore(s.tree).Load(ctx, sess.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ag := s.newAgent(nil, sess, cfg, nil, s.offeredMCP(), opts)
	next, err := ag.Preview(loaded)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	calibration, err := s.lastCalibration(ctx, sess.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, contextResponse{
		Context:       next,
		Sources:       cfg.parameterSources(),
		DroppedEffort: cfg.droppedEffort,
		Calibration:   calibration,
		ContextWindow: cfg.model.ContextWindow,
	})
}

// handleSessionCalls lists the records of a session's model calls.
func (s *Server) handleSessionCalls(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	records, err := s.deps.Store.ModelCalls(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := callsResponse{SessionID: id, Calls: make([]modelCallBody, 0, len(records))}
	for _, rec := range records {
		out.Calls = append(out.Calls, asModelCall(rec))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleSessionCall returns one recorded model call in the shape of the
// preview, its messages rebuilt from the session's path down to the entry
// the call's conversation ended at.
func (s *Server) handleSessionCall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec, err := s.deps.Store.ModelCall(ctx, r.PathValue("id"), r.PathValue("call_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	call, params, err := recordedContext(rec)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var entries []store.Entry
	if rec.EntryID != "" {
		if entries, err = s.deps.Store.EntryPath(ctx, rec.SessionID, rec.EntryID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	messages, err := session.Messages(entries)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	call = call.WithMessages(messages)
	body := asModelCall(rec)
	out := contextResponse{
		Context:       call,
		Sources:       params.Sources,
		DroppedEffort: params.DroppedEffort,
		Call:          &body,
	}
	if out.Sources == nil {
		out.Sources = map[string]string{}
	}
	if rec.ModelID != "" {
		m, err := s.deps.Store.Model(ctx, rec.ModelID)
		switch {
		case err == nil:
			out.ContextWindow = m.ContextWindow
		case !errors.Is(err, store.ErrNotFound):
			s.fail(w, r, err)
			return
		}
	}
	if measured := rec.PromptTokens; measured != nil && *measured > 0 {
		out.Calibration = &calibrationBody{CallID: rec.ID, PromptTokens: *measured, EstimatedTokens: estimatedTokens(call)}
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// lastCalibration returns the session's last model call the endpoint
// measured, beside the estimate of what it sent, or nil when it has none.
func (s *Server) lastCalibration(ctx context.Context, sessionID string) (*calibrationBody, error) {
	records, err := s.deps.Store.ModelCalls(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		if in := records[i].PromptTokens; in == nil || *in == 0 {
			continue
		}
		rec, err := s.deps.Store.ModelCall(ctx, sessionID, records[i].ID)
		if err != nil {
			return nil, err
		}
		call, _, err := recordedContext(rec)
		if err != nil {
			return nil, err
		}
		call.MessageTokens = rec.MessageTokens
		return &calibrationBody{CallID: rec.ID, PromptTokens: *records[i].PromptTokens, EstimatedTokens: estimatedTokens(call)}, nil
	}
	return nil, nil
}

// recordedContext decodes what a record kept of its call: the sections,
// the tool schemas, and the parameters, without the messages.
func recordedContext(rec store.ModelCall) (agent.Context, recordedParameters, error) {
	var (
		call   agent.Context
		params recordedParameters
	)
	if err := json.Unmarshal(rec.Sections, &call.Sections); err != nil {
		return agent.Context{}, recordedParameters{}, fmt.Errorf("decode the sections of call %s: %w", rec.ID, err)
	}
	if err := json.Unmarshal(rec.Tools, &call.Tools); err != nil {
		return agent.Context{}, recordedParameters{}, fmt.Errorf("decode the tools of call %s: %w", rec.ID, err)
	}
	if err := json.Unmarshal(rec.Parameters, &params); err != nil {
		return agent.Context{}, recordedParameters{}, fmt.Errorf("decode the parameters of call %s: %w", rec.ID, err)
	}
	call.Parameters = params.Parameters
	return call, params, nil
}

// estimatedTokens is the estimated size of everything a call sends.
func estimatedTokens(c agent.Context) int {
	total := c.MessageTokens
	for _, s := range c.Sections {
		total += s.Tokens
	}
	for _, t := range c.Tools {
		total += t.Tokens
	}
	return total
}

// asModelCall renders a record on the wire.
func asModelCall(c store.ModelCall) modelCallBody {
	return modelCallBody{
		ID:                    c.ID,
		SessionID:             c.SessionID,
		RunID:                 c.RunID,
		EntryID:               c.EntryID,
		ModelID:               c.ModelID,
		Model:                 c.Model,
		MessageTokens:         c.MessageTokens,
		PromptTokens:          c.PromptTokens,
		CompletionTokens:      c.CompletionTokens,
		TotalTokens:           c.TotalTokens,
		CachedTokens:          c.CachedTokens,
		PrefilledTokens:       c.PrefilledTokens,
		ReasoningTokens:       c.ReasoningTokens,
		ReasoningSource:       c.ReasoningSource,
		PredictedPromptTokens: c.PredictedPromptTokens,
		Prediction:            c.Prediction,
		Drift:                 c.Drift,
		ContextWindow:         c.ContextWindow,
		Endpoint:              c.Endpoint,
		QueueMS:               c.QueueMS,
		Slot:                  c.Slot,
		Class:                 c.Class,
		Priority:              c.Priority,
		Agent:                 c.Agent,
		CreatedAt:             c.CreatedAt,
	}
}
