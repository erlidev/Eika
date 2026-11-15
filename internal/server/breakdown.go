package server

import (
	"context"
	"net/http"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// The sources a breakdown's prompt count can have.
const (
	// promptMeasured is the prompt count the endpoint stated for a recorded
	// call.
	promptMeasured = "measured"
	// promptPredicted is an exact prediction, which for the next request stands
	// in for a measurement that does not exist yet: the endpoint rendered and
	// tokenised that very request.
	promptPredicted = "predicted"
)

// breakdownBody is where the tokens of a request go, part by part, each
// counted on its own and none scaled to fit (docs/design/runtime.md 2). The
// part lists are in the order of the request's sections, tools and messages.
type breakdownBody struct {
	agent.Breakdown
	// PromptTokens is the whole request's size, which the parts fall short of by
	// the cost of the template and the formatting: the call's measured prompt,
	// or for the next request an exact prediction. Null when there is none, and
	// never 0 in place of one.
	PromptTokens *int `json:"prompt_tokens"`
	// PromptSource says where PromptTokens came from, measured or predicted. It
	// is absent exactly when PromptTokens is null.
	PromptSource string `json:"prompt_source,omitempty"`
	// Remainder is PromptTokens less Parts: what the template and formatting
	// cost. It is negative when the parts add up to more than the endpoint
	// counted, which says a part was counted wrongly, and it is never clamped.
	// Null exactly when PromptTokens is.
	Remainder *int `json:"remainder"`
}

// newBreakdown states the remainder of parts against a prompt count, which is
// nil when there is none.
func newBreakdown(parts agent.Breakdown, prompt *int, source string) *breakdownBody {
	b := &breakdownBody{Breakdown: parts}
	if prompt != nil {
		b.PromptTokens, b.PromptSource = prompt, source
		b.Remainder = new(*prompt - parts.Parts)
	}
	return b
}

// predictionBody is what the token counter expects a request to cost, and how
// it knows.
type predictionBody struct {
	// Tokens is the expected prompt size.
	Tokens int `json:"tokens"`
	// Method is exact, anchored or estimated.
	Method string `json:"method"`
}

// predictionResponse is the body of GET /api/sessions/{id}/prediction: the
// size of the next request of a session without the request itself.
type predictionResponse struct {
	// Model is the name of the model the prediction is for.
	Model string `json:"model"`
	predictionBody
	// ContextWindow is the effective window of the model, in tokens.
	ContextWindow int `json:"context_window"`
}

// counterFor returns the token counter a preview of a session counts with,
// with the routes of the model's endpoint attached until release is called.
// It is the counter the session's runs use, so what a run learned and
// remembered is not counted again. A model that is unknown, or whose endpoint
// cannot be built, gets a counter that estimates, whose answers say so.
func (s *Server) counterFor(ctx context.Context, sessionID string, m store.Model, known bool) (*counter.Counter, func()) {
	if known {
		ep, err := s.endpointOf(ctx, m)
		if err == nil {
			return s.counters.forRun(sessionID, ep, m)
		}
		s.log.Warn("count a preview with an estimate: endpoint unavailable", "session_id", sessionID, "model", m.Name, "error", err)
	}
	return counter.NewEstimating(), func() {}
}

// previewNext assembles the request the next run of sess would send, and the
// token counter that sizes it, with the counter's routes attached until
// release is called.
func (s *Server) previewNext(ctx context.Context, sess store.Session, cfg runConfig) (next agent.Context, ctr *counter.Counter, release func(), err error) {
	loaded, err := session.NewStore(s.tree).Load(ctx, sess.ID)
	if err != nil {
		return agent.Context{}, nil, nil, err
	}
	if sess.Agent != "" && loaded.Preamble == "" {
		loaded.Preamble, err = s.buildPreamble(ctx, sess)
		if err != nil {
			return agent.Context{}, nil, nil, err
		}
	}
	cfg = cfg.withSnapshot(loaded)
	ctr, release = s.counterFor(ctx, sess.ID, cfg.model, cfg.modelOK)
	ag := s.newAgent(nil, sess, cfg, nil, s.offeredMCP(), agent.Options{Logger: s.log, Counter: ctr})
	if next, err = ag.Preview(loaded); err != nil {
		release()
		return agent.Context{}, nil, nil, err
	}
	return next, ctr, release, nil
}

// predictedNext is the counter's size of the next request, bounded by the
// counting timeout. It leaves nothing behind for a run's measurement to meet.
func predictedNext(ctx context.Context, ctr *counter.Counter, next agent.Context) predictionBody {
	countCtx, cancel := context.WithTimeout(ctx, agent.DefaultCountTimeout)
	defer cancel()
	pred := ctr.Peek(countCtx, next.Request())
	return predictionBody{Tokens: pred.Tokens, Method: string(pred.Method)}
}

// breakdownOfNext counts the parts of the next request. Its remainder is
// stated against the prediction when that is exact, since the endpoint's own
// count of exactly this request is what the next run would measure.
func breakdownOfNext(ctx context.Context, ctr *counter.Counter, next agent.Context, pred predictionBody) *breakdownBody {
	countCtx, cancel := context.WithTimeout(ctx, agent.DefaultCountTimeout)
	defer cancel()
	parts := agent.MeasureParts(countCtx, ctr, next)
	if pred.Method == string(provider.MethodExact) {
		return newBreakdown(parts, &pred.Tokens, promptPredicted)
	}
	return newBreakdown(parts, nil, "")
}

// handleSessionPrediction answers what the next request of a session is
// expected to cost and how that is known, for the meter. It sends none of the
// request, which for a long session is most of a context response.
func (s *Server) handleSessionPrediction(w http.ResponseWriter, r *http.Request) {
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
	if !cfg.modelOK {
		s.fail(w, r, conflictf("no model is configured; add one under Settings, Models"))
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
	writeJSON(w, s.log, http.StatusOK, predictionResponse{
		Model:          cfg.model.Name,
		predictionBody: predictedNext(ctx, ctr, next),
		ContextWindow:  cfg.window(),
	})
}
