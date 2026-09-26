package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// modelRequestBody is the record of one model call on the wire, without
// what it sent.
type modelRequestBody struct {
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
	// InputTokens, OutputTokens, and TotalTokens are what the endpoint
	// measured, zero when it reported nothing.
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	CreatedAt    time.Time `json:"created_at"`
}

// requestsResponse is the body of GET /api/sessions/{id}/requests.
type requestsResponse struct {
	SessionID string `json:"session_id"`
	// Requests are the session's recorded model calls, oldest first.
	Requests []modelRequestBody `json:"requests"`
}

// calibrationBody is one measured model call beside the estimate of what it
// sent, so an estimate can be scaled to what the endpoint counts.
type calibrationBody struct {
	RequestID string `json:"request_id"`
	// InputTokens is what the endpoint measured the call's input as.
	InputTokens int `json:"input_tokens"`
	// EstimatedTokens is the estimate of the same input: its sections, tool
	// schemas, and messages.
	EstimatedTokens int `json:"estimated_tokens"`
}

// contextResponse is the body of GET /api/sessions/{id}/context, the next
// model call of the session, and of GET /api/sessions/{id}/requests/{id}, a
// recorded one: the system prompt by section, the tool schemas, the
// messages, and the parameters, with the layer each parameter came from.
type contextResponse struct {
	agent.Context
	// Sources names the layer each parameter came from, keyed model,
	// thinking_switch, preserve_thinking, and sampling.<parameter>.
	Sources map[string]string `json:"sources"`
	// DroppedEffort is a reasoning effort the configuration chose that the
	// model does not offer, which is not sent.
	DroppedEffort string `json:"dropped_effort,omitempty"`
	// Request is the record this is, absent for the next call.
	Request *modelRequestBody `json:"request,omitempty"`
	// ContextWindow is the context window of the call's model, in tokens:
	// what the request has to fit in. Zero when the model is not known,
	// because none is configured or a recorded call's model is deleted.
	ContextWindow int `json:"context_window"`
	// ContextFilesUnread says why the preview holds no context files where
	// a run would read them: the session's workspace is not running, so
	// they cannot be read until it starts. Absent when they were read, or
	// when the run would not read them.
	ContextFilesUnread string `json:"context_files_unread,omitempty"`
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
// model_requests table.
type callRecorder struct {
	store  *store.Store
	run    *activeRun
	config runConfig
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
	_, err = c.store.RecordModelRequest(ctx, store.ModelRequest{
		SessionID:     call.SessionID,
		RunID:         c.run.runID(),
		EntryID:       sess.HeadEntryID,
		ModelID:       c.config.model.ID,
		Model:         c.config.model.Name,
		Sections:      sections,
		Tools:         tools,
		Parameters:    parameters,
		MessageTokens: call.Context.MessageTokens,
		InputTokens:   call.Usage.InputTokens,
		OutputTokens:  call.Usage.OutputTokens,
		TotalTokens:   call.Usage.TotalTokens,
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
	// The context files are read from the workspace as a run reads them,
	// which needs it running. A stopped workspace still has a preview,
	// with the workspace's base prompt and the files marked unread, since
	// everything else in it is known without the workspace.
	var ex executor.Executor
	var unread string
	opts := agent.Options{Logger: s.log}
	if !sess.Chat() {
		ex, err = s.executorFor(ctx, sess.WorkspaceID)
		if status, _ := statusOf(err); err != nil && status != http.StatusConflict {
			s.fail(w, r, err)
			return
		}
		if err != nil && cfg.contextFiles {
			unread = err.Error()
		}
	}
	loaded, err := session.NewStore(s.tree, nil).Load(ctx, sess.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ag := s.newAgent(nil, sess, cfg, ex, s.offeredMCP(), opts)
	next, err := ag.Preview(ctx, loaded)
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
		Context:            next,
		Sources:            cfg.parameterSources(),
		DroppedEffort:      cfg.droppedEffort,
		ContextFilesUnread: unread,
		Calibration:        calibration,
		ContextWindow:      cfg.model.ContextWindow,
	})
}

// handleSessionRequests lists the records of a session's model calls.
func (s *Server) handleSessionRequests(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	records, err := s.deps.Store.ModelRequests(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := requestsResponse{SessionID: id, Requests: make([]modelRequestBody, 0, len(records))}
	for _, rec := range records {
		out.Requests = append(out.Requests, asModelRequest(rec))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleSessionRequest returns one recorded model call in the shape of the
// preview, its messages rebuilt from the session's path down to the entry
// the call's conversation ended at.
func (s *Server) handleSessionRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rec, err := s.deps.Store.ModelRequest(ctx, r.PathValue("id"), r.PathValue("request_id"))
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
	body := asModelRequest(rec)
	out := contextResponse{
		Context:       call,
		Sources:       params.Sources,
		DroppedEffort: params.DroppedEffort,
		Request:       &body,
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
	if rec.InputTokens > 0 {
		out.Calibration = &calibrationBody{RequestID: rec.ID, InputTokens: rec.InputTokens, EstimatedTokens: estimatedTokens(call)}
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// lastCalibration returns the session's last model call the endpoint
// measured, beside the estimate of what it sent, or nil when it has none.
func (s *Server) lastCalibration(ctx context.Context, sessionID string) (*calibrationBody, error) {
	records, err := s.deps.Store.ModelRequests(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].InputTokens == 0 {
			continue
		}
		rec, err := s.deps.Store.ModelRequest(ctx, sessionID, records[i].ID)
		if err != nil {
			return nil, err
		}
		call, _, err := recordedContext(rec)
		if err != nil {
			return nil, err
		}
		call.MessageTokens = rec.MessageTokens
		return &calibrationBody{RequestID: rec.ID, InputTokens: rec.InputTokens, EstimatedTokens: estimatedTokens(call)}, nil
	}
	return nil, nil
}

// recordedContext decodes what a record kept of its call: the sections,
// the tool schemas, and the parameters, without the messages.
func recordedContext(rec store.ModelRequest) (agent.Context, recordedParameters, error) {
	var (
		call   agent.Context
		params recordedParameters
	)
	if err := json.Unmarshal(rec.Sections, &call.Sections); err != nil {
		return agent.Context{}, recordedParameters{}, fmt.Errorf("decode the sections of request %s: %w", rec.ID, err)
	}
	if err := json.Unmarshal(rec.Tools, &call.Tools); err != nil {
		return agent.Context{}, recordedParameters{}, fmt.Errorf("decode the tools of request %s: %w", rec.ID, err)
	}
	if err := json.Unmarshal(rec.Parameters, &params); err != nil {
		return agent.Context{}, recordedParameters{}, fmt.Errorf("decode the parameters of request %s: %w", rec.ID, err)
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

// asModelRequest renders a record on the wire.
func asModelRequest(r store.ModelRequest) modelRequestBody {
	return modelRequestBody{
		ID:            r.ID,
		SessionID:     r.SessionID,
		RunID:         r.RunID,
		EntryID:       r.EntryID,
		ModelID:       r.ModelID,
		Model:         r.Model,
		MessageTokens: r.MessageTokens,
		InputTokens:   r.InputTokens,
		OutputTokens:  r.OutputTokens,
		TotalTokens:   r.TotalTokens,
		CreatedAt:     r.CreatedAt,
	}
}
