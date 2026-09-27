package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// settingCompaction configures how a run compacts a conversation that
// outgrows its model's context window.
const settingCompaction = "compaction"

// The bounds the compaction budgets accept. A budget below the minimum
// leaves no room for a useful summary; the window caps both at run time.
const (
	minCompactionTokens = 1024
	maxCompactionTokens = 1 << 20
)

// compactionBody is the compaction setting. A field the stored value leaves
// out keeps its default, and an empty prompt is the built-in one.
type compactionBody struct {
	// Auto compacts before a request that would leave less than
	// ReserveTokens of the window free, and after an endpoint refuses a
	// request as too large for it.
	Auto bool `json:"auto"`
	// ReserveTokens is how much of the window stays free.
	ReserveTokens int `json:"reserve_tokens"`
	// KeepRecentTokens is roughly how much of the newest conversation stays
	// verbatim.
	KeepRecentTokens int                   `json:"keep_recent_tokens"`
	Prompts          compactionPromptsBody `json:"prompts"`
}

// compactionPromptsBody are the prompts that ask for a summary.
type compactionPromptsBody struct {
	// Summary asks for the first summary of a conversation.
	Summary string `json:"summary"`
	// Update asks for a summary that folds newer messages into the last one.
	Update string `json:"update"`
	// TurnPrefix asks for the summary of the start of a turn too large to
	// keep whole.
	TurnPrefix string `json:"turn_prefix"`
}

// compactRequest is the body of POST /api/sessions/{id}/compact.
type compactRequest struct {
	// Instructions say what the summary should focus on.
	Instructions string `json:"instructions"`
	// Model names the model that writes the summary. Empty uses the one a
	// run would.
	Model string `json:"model"`
}

// defaultCompaction is the compaction setting until the user sets it, with
// empty prompts, which are the built-in ones.
func defaultCompaction() compactionBody {
	return compactionBody{
		Auto:             true,
		ReserveTokens:    agent.DefaultReserveTokens,
		KeepRecentTokens: agent.DefaultKeepRecentTokens,
	}
}

// compactionDefaults is the compaction setting's default as the settings
// response reports it: with the built-in prompts spelled out, so that an
// editor can show them.
func compactionDefaults() compactionBody {
	c := defaultCompaction()
	c.Prompts = compactionPromptsBody{
		Summary:    agent.SummaryPrompt,
		Update:     agent.UpdatePrompt,
		TurnPrefix: agent.TurnPrefixPrompt,
	}
	return c
}

// validateCompaction rejects a compaction setting the harness could not use.
func validateCompaction(value json.RawMessage) error {
	c := defaultCompaction()
	if err := strictJSON(value, &c); err != nil {
		return invalidf("%s must be an object of auto, reserve_tokens, keep_recent_tokens, and prompts", settingCompaction)
	}
	for field, n := range map[string]int{"reserve_tokens": c.ReserveTokens, "keep_recent_tokens": c.KeepRecentTokens} {
		if n < minCompactionTokens || n > maxCompactionTokens {
			return invalidf("%s.%s must be from %d to %d", settingCompaction, field, minCompactionTokens, maxCompactionTokens)
		}
	}
	for field, text := range map[string]string{"summary": c.Prompts.Summary, "update": c.Prompts.Update, "turn_prefix": c.Prompts.TurnPrefix} {
		if len(text) > maxPromptBytes {
			return invalidf("%s.prompts.%s must be at most %d bytes", settingCompaction, field, maxPromptBytes)
		}
	}
	return nil
}

// compaction returns how a run with model m compacts its conversation.
func (s *Server) compaction(ctx context.Context, m store.Model) agent.CompactionSettings {
	c := defaultCompaction()
	readSetting(ctx, s.deps.Store, s.log, settingCompaction, &c)
	return agent.CompactionSettings{
		Auto:             c.Auto,
		ReserveTokens:    c.ReserveTokens,
		KeepRecentTokens: c.KeepRecentTokens,
		Prompts: agent.CompactionPrompts{
			Summary:    c.Prompts.Summary,
			Update:     c.Prompts.Update,
			TurnPrefix: c.Prompts.TurnPrefix,
		},
		FallbackEffort: fallbackEffort(m),
	}
}

// effortsLowestFirst are the reasoning efforts whose rank is known, lowest
// first. An effort the endpoint names in a word of its own has no known
// rank.
var effortsLowestFirst = []string{provider.EffortNone, "minimal", "low", "medium", "high", "xhigh", "max"}

// fallbackEffort is the reasoning effort a summary is asked for again with
// when thinking used up its output budget: the lowest the model offers, which
// turns thinking off when the model offers none. A model that offers no
// effort of a known rank has no fallback.
func fallbackEffort(m store.Model) string {
	for _, effort := range effortsLowestFirst {
		if slices.Contains(m.ReasoningEfforts, effort) {
			return effort
		}
	}
	return ""
}

// handleCompact summarizes the older part of a session's conversation now,
// as a run of its own, which the client follows on the event stream. A
// conversation with nothing old enough to summarize is a conflict.
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[compactRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Instructions) > maxPromptBytes {
		s.fail(w, r, invalidf("instructions must be at most %d bytes", maxPromptBytes))
		return
	}
	run, err := s.runs.compact(r.Context(), r.PathValue("id"), req.Instructions, req.Model)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusAccepted, asRun(run))
}
