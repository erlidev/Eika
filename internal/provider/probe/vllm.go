package probe

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Not verified against a live server (none is on the bench): the routes and
// fields below follow vLLM's documentation and source, and probetest/testdata/
// vllm holds fixtures written from them.

// vllm identifies a vLLM server by /version, confirmed by its metrics page or
// by the max_model_len it adds to every model in /v1/models (SGLang, checked
// first, has /version too).
//
// A request may use max_model_len, whichever model it names. The KV cache is
// one pool of paged blocks, so it is Shared, and its size is the blocks the
// engine allocated times the tokens in a block, which /metrics states in the
// labels of vllm:cache_config_info. The size is zero when metrics are off.
// vLLM states no slot count (max_num_seqs is not exported), so the default
// applies until the user sets one.
func (p *prober) vllm(ctx context.Context) (Result, bool) {
	var version struct {
		Version string `json:"version"`
	}
	if !p.getJSON(ctx, "/version", &version) || version.Version == "" {
		return Result{}, false
	}
	metrics, haveMetrics := p.getText(ctx, "/metrics")
	window := p.modelWindow()
	if (!haveMetrics || !strings.Contains(metrics, "vllm:")) && window == 0 {
		return Result{}, false
	}
	res := Result{
		Backend: VLLM,
		Slots:   DefaultSlots(VLLM),
		KV:      Shared,
		Capabilities: Capabilities{
			Build:  version.Version,
			Window: window,
			// usage.prompt_tokens_details.cached_tokens needs the server's
			// --enable-prompt-tokens-details, which a probe cannot see.
			CacheMetrics: CacheUnknown,
		},
	}
	res.KVTokens = vllmCacheTokens(metrics)
	res.Capabilities.Counting = p.vllmCounting(ctx)
	return res, true
}

var (
	cacheConfigInfo = regexp.MustCompile(`(?m)^vllm:cache_config_info\{([^}]*)\}`)
	labelNumber     = func(name string) *regexp.Regexp { return regexp.MustCompile(name + `="(\d+)"`) }
	gpuBlocksLabel  = labelNumber("num_gpu_blocks")
	blockSizeLabel  = labelNumber("block_size")
)

// vllmCacheTokens reads the KV cache size, in tokens, from the labels of
// vllm:cache_config_info: num_gpu_blocks times block_size. It is zero when
// the page has no such series or the engine has not sized its cache yet
// (num_gpu_blocks="None").
func vllmCacheTokens(metrics string) int {
	m := cacheConfigInfo.FindStringSubmatch(metrics)
	if m == nil {
		return 0
	}
	blocks := label(gpuBlocksLabel, m[1])
	size := label(blockSizeLabel, m[1])
	return blocks * size
}

// label reads the number a label holds, or zero.
func label(re *regexp.Regexp, labels string) int {
	m := re.FindStringSubmatch(labels)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// vllmCounting checks /tokenize with messages, which renders the chat
// template, and with a prompt, which does not.
func (p *prober) vllmCounting(ctx context.Context) Counting {
	var out struct {
		Tokens []any `json:"tokens"`
	}
	request := func(field string, value any) map[string]any {
		body := map[string]any{field: value}
		if model := p.firstModel(); model != "" {
			body["model"] = model
		}
		return body
	}
	if p.postJSON(ctx, "/tokenize", request("messages", probeMessages), &out) && len(out.Tokens) > 0 {
		return CountChat
	}
	out.Tokens = nil
	if p.postJSON(ctx, "/tokenize", request("prompt", "hi"), &out) && len(out.Tokens) > 0 {
		return CountText
	}
	return CountNone
}
