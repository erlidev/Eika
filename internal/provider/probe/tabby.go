package probe

import "context"

// Not verified against a live server (the bench's TabbyAPI is not running):
// the routes and fields below follow TabbyAPI's documentation and source, and
// probetest/testdata/tabby holds a fixture written from them.

// tabby identifies a TabbyAPI server by /v1/model, which describes the loaded
// model with the engine's parameters beside it.
//
// parameters.max_seq_len is the window of one request. exllamav3 allocates one
// cache of parameters.cache_size tokens that the batch shares, so the cache is
// Shared. Its batch size (max_batch_size) is the slot count when it is set.
// TabbyAPI reports no cache metrics in a response that is known to this probe.
func (p *prober) tabby(ctx context.Context) (Result, bool) {
	var model struct {
		Parameters struct {
			MaxSeqLen    int `json:"max_seq_len"`
			CacheSize    int `json:"cache_size"`
			MaxBatchSize int `json:"max_batch_size"`
		} `json:"parameters"`
	}
	if !p.getJSON(ctx, "/v1/model", &model) || model.Parameters.MaxSeqLen == 0 {
		return Result{}, false
	}
	res := Result{
		Backend:  Tabby,
		Slots:    model.Parameters.MaxBatchSize,
		KV:       Shared,
		KVTokens: model.Parameters.CacheSize,
		Capabilities: Capabilities{
			Window:       model.Parameters.MaxSeqLen,
			CacheMetrics: CacheUnknown,
			Counting:     p.tabbyCounting(ctx),
		},
	}
	if res.Slots <= 0 {
		res.Slots = DefaultSlots(Tabby)
	}
	return res, true
}

// tabbyCounting checks /v1/token/encode, which takes text or a list of chat
// messages that it renders with the model's template.
func (p *prober) tabbyCounting(ctx context.Context) Counting {
	var out struct {
		Tokens []any `json:"tokens"`
	}
	if p.postJSON(ctx, "/v1/token/encode", map[string]any{"text": probeMessages}, &out) && len(out.Tokens) > 0 {
		return CountChat
	}
	out.Tokens = nil
	if p.postJSON(ctx, "/v1/token/encode", map[string]any{"text": "hi"}, &out) && len(out.Tokens) > 0 {
		return CountText
	}
	return CountNone
}
