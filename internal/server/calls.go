package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
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
	// QueueMS is how long the call waited for a lease on its pool, Slot the
	// slot of the server the request named (id_slot), null when it named none
	// because its endpoint has no slots to name, Class the model class the run
	// was resolved from and Priority what the call asked for: interactive,
	// foreground, background or maintenance. They are null or absent for a
	// call recorded before the queue existed, and Class for a run that named
	// its model. Agent is null until the agents fill it.
	QueueMS  *int   `json:"queue_ms"`
	Slot     *int   `json:"slot"`
	Class    string `json:"class,omitempty"`
	Priority string `json:"priority,omitempty"`
	Agent    string `json:"agent,omitempty"`
	// PrefillMS is how long the endpoint took to read the prompt, as it
	// stated it, and DecodeMS how long it spent generating: its own measure,
	// else the harness's from the first token. Null when nothing measured
	// them.
	PrefillMS *int      `json:"prefill_ms"`
	DecodeMS  *int      `json:"decode_ms"`
	CreatedAt time.Time `json:"created_at"`
}

// callsResponse is the body of GET /api/sessions/{id}/calls.
type callsResponse struct {
	SessionID string `json:"session_id"`
	// Calls are the session's recorded model calls, oldest first.
	Calls []modelCallBody `json:"calls"`
}

// contextResponse is the body of GET /api/sessions/{id}/context, the next
// model call of the session, and of GET /api/sessions/{id}/calls/{id}, a
// recorded one: the system prompt by section, the tool schemas, the
// messages, and the parameters, with the layer each parameter came from, and
// how many tokens each part is.
//
// The agent's own size estimates (a section's, a tool's and a message's
// tokens, four bytes to a token) are what the loop plans a compaction cut
// with. They are on the wire as they are and a client shows Breakdown instead.
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
	// ContextWindow is the effective window, in tokens: what the request has
	// to fit in. For a recorded call it is the window the call was judged
	// against. Zero when the model is not known, because none is configured
	// or a recorded call kept no window and its model is deleted.
	ContextWindow int `json:"context_window"`
	// Breakdown is where the request's tokens go, part by part.
	Breakdown *breakdownBody `json:"breakdown,omitempty"`
	// Prediction is what the token counter expects the next request to cost,
	// and how it knows. Absent for a recorded call, which carries its own on
	// Call.
	Prediction *predictionBody `json:"prediction,omitempty"`
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

// predictedTokens is the size a prediction stated, nil for a call that was
// not predicted, whose prediction is the zero value.
func predictedTokens(p provider.Prediction) *int {
	if p.Method == "" {
		return nil
	}
	return &p.Tokens
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

		PromptTokens:          call.Usage.InputTokens.Ptr(),
		CompletionTokens:      call.Usage.OutputTokens.Ptr(),
		TotalTokens:           call.Usage.TotalTokens.Ptr(),
		CachedTokens:          call.Usage.CachedTokens.Ptr(),
		PrefilledTokens:       call.Usage.Prefilled().Ptr(),
		ReasoningTokens:       call.Reasoning.Ptr(),
		ReasoningSource:       call.ReasoningSource,
		PredictedPromptTokens: predictedTokens(call.Prediction),
		Prediction:            string(call.Prediction.Method),
		Drift:                 call.Drift,
		ContextWindow:         positive(call.ContextWindow),
		Endpoint:              c.endpoint,
		QueueMS:               queueMS(call.Queue),
		Slot:                  slotOf(call.Queue),
		Class:                 call.Queue.Class,
		Priority:              call.Queue.Priority,
		PrefillMS:             prefillMS(call.Timings),
		DecodeMS:              decodeMS(call.Timings),
	})
	return err
}

// queueMS is the time a call waited in the inference queue, nil for a call
// that took no lease.
func queueMS(q agent.QueueRecord) *int {
	if !q.Queued {
		return nil
	}
	return new(int(q.Waited.Milliseconds()))
}

// slotOf is the slot of the server a call named, nil for a call that named
// none: one that took no lease, or ran on an endpoint with no slots to name.
func slotOf(q agent.QueueRecord) *int {
	if !q.Queued {
		return nil
	}
	return q.Slot
}

// prefillMS is the time the endpoint said it took to read the prompt, nil
// when it said none.
func prefillMS(t provider.Timings) *int {
	if !t.HasPrompt() {
		return nil
	}
	return new(int(math.Round(t.PromptMS)))
}

// decodeMS is the time spent generating, nil when nothing measured it.
func decodeMS(t provider.Timings) *int {
	if !t.HasDecode() {
		return nil
	}
	return new(int(math.Round(t.DecodeMS)))
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
	if cfg, err = s.probed(ctx, cfg); err != nil {
		s.fail(w, r, err)
		return
	}
	next, ctr, release, err := s.previewNext(ctx, sess, cfg)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer release()
	pred := predictedNext(ctx, ctr, next)
	writeJSON(w, s.log, http.StatusOK, contextResponse{
		Context:       next,
		Sources:       cfg.parameterSources(),
		DroppedEffort: cfg.droppedEffort,
		ContextWindow: cfg.window(),
		Breakdown:     breakdownOfNext(ctx, ctr, next, pred),
		Prediction:    &pred,
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
	// The call's model may be gone, which leaves it without an endpoint to count
	// with: its parts are then estimated, and say so.
	var model store.Model
	modelKnown := false
	if rec.ModelID != "" {
		m, err := s.deps.Store.Model(ctx, rec.ModelID)
		switch {
		case err == nil:
			model, modelKnown = m, true
		case !errors.Is(err, store.ErrNotFound):
			s.fail(w, r, err)
			return
		}
	}
	// The window the call was judged against is the one it recorded; a record
	// that kept none falls back to what its model is configured with.
	switch {
	case rec.ContextWindow != nil:
		out.ContextWindow = *rec.ContextWindow
	case modelKnown:
		out.ContextWindow = model.ContextWindow
	}
	ctr, release := s.counterFor(ctx, rec.SessionID, model, modelKnown)
	defer release()
	countCtx, cancel := context.WithTimeout(ctx, agent.DefaultCountTimeout)
	defer cancel()
	parts := agent.MeasureParts(countCtx, ctr, call)
	var prompt *int
	if measured := rec.PromptTokens; measured != nil && *measured > 0 {
		prompt = measured
	}
	out.Breakdown = newBreakdown(parts, prompt, promptMeasured)
	writeJSON(w, s.log, http.StatusOK, out)
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
		PrefillMS:             c.PrefillMS,
		DecodeMS:              c.DecodeMS,
		CreatedAt:             c.CreatedAt,
	}
}
