package probe

import "context"

// Not verified against a live server (none is on the bench): the routes and
// fields below follow SGLang's documentation and source, and probetest/
// testdata/sglang holds a fixture written from them.

// sglang identifies an SGLang server by /get_server_info (or /server_info,
// its newer name), which states the server arguments and the scheduler's
// sizing.
//
// max_total_num_tokens is the size of the radix-cached KV pool every request
// draws on, so the cache is Shared. context_length is null when the server
// uses the model's own, in which case the model list states the window.
// max_running_requests is the slot count when set.
func (p *prober) sglang(ctx context.Context) (Result, bool) {
	var info struct {
		Version            string `json:"version"`
		ContextLength      int    `json:"context_length"`
		MaxTotalNumTokens  int    `json:"max_total_num_tokens"`
		MaxRunningRequests int    `json:"max_running_requests"`
	}
	if !p.getJSON(ctx, "/get_server_info", &info) && !p.getJSON(ctx, "/server_info", &info) {
		return Result{}, false
	}
	// An answer with none of these fields is some other server's JSON.
	if info.Version == "" && info.ContextLength == 0 && info.MaxTotalNumTokens == 0 {
		return Result{}, false
	}
	window := info.ContextLength
	if window == 0 {
		window = p.modelWindow()
	}
	res := Result{
		Backend:  SGLang,
		Slots:    info.MaxRunningRequests,
		KV:       Shared,
		KVTokens: info.MaxTotalNumTokens,
		Capabilities: Capabilities{
			Build:        info.Version,
			Window:       window,
			CacheMetrics: CacheUnknown,
			Counting:     p.sglangCounting(ctx),
		},
	}
	if res.Slots <= 0 {
		res.Slots = DefaultSlots(SGLang)
	}
	return res, true
}

// sglangCounting checks /tokenize, which takes text only.
func (p *prober) sglangCounting(ctx context.Context) Counting {
	body := map[string]any{"prompt": "hi"}
	if model := p.firstModel(); model != "" {
		body["model"] = model
	}
	var out struct {
		Tokens []any `json:"tokens"`
	}
	if p.postJSON(ctx, "/tokenize", body, &out) && len(out.Tokens) > 0 {
		return CountText
	}
	return CountNone
}
