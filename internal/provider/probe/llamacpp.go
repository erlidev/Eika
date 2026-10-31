package probe

import (
	"context"
	"path"
	"strings"
)

// llamaCpp identifies a llama.cpp server by /props and reads it. Checked
// against build b11942.
//
// /props states the build, the slot count and the per-slot context
// (default_generation_settings.n_ctx, which /slots repeats for every slot). It
// does not state whether the KV cache is unified (--kv-unified) or the total
// context (-c): a unified server reports its whole cache as every slot's
// context, and a partitioned one reports -c divided by -np, so 2 slots of
// 131072 read the same for "-c 131072 --kv-unified" and for "-c 262144
// --no-kv-unified". Nothing else the server serves tells them apart either
// (/slots, /metrics and /v1/models were read), so with more than one slot the
// cache is Unknown and the user states it. A single slot owns the whole cache
// either way, which is Shared.
func (p *prober) llamaCpp(ctx context.Context) (Result, bool) {
	var props struct {
		BuildInfo  string          `json:"build_info"`
		ModelPath  string          `json:"model_path"`
		ModelFtype string          `json:"model_ftype"`
		TotalSlots int             `json:"total_slots"`
		Modalities map[string]bool `json:"modalities"`
		Defaults   struct {
			Context int `json:"n_ctx"`
		} `json:"default_generation_settings"`
		EndpointSlots   bool `json:"endpoint_slots"`
		EndpointMetrics bool `json:"endpoint_metrics"`
	}
	if !p.getJSON(ctx, "/props", &props) || (props.BuildInfo == "" && props.TotalSlots == 0) {
		return Result{}, false
	}
	res := Result{
		Backend: LlamaCpp,
		Slots:   props.TotalSlots,
		KV:      Unknown,
		Capabilities: Capabilities{
			Build:  props.BuildInfo,
			Window: props.Defaults.Context,
			// timings.cache_n in the stream, and usage.prompt_tokens_details.
			// cached_tokens, which equals it (checked on b11942).
			CacheMetrics: CacheReported,
			SlotHints:    true,
		},
		Details: Details{
			Quantisation:    props.ModelFtype,
			Vision:          props.Modalities["vision"],
			SlotsEndpoint:   props.EndpointSlots,
			MetricsEndpoint: props.EndpointMetrics,
		},
	}
	if res.Slots <= 0 {
		res.Slots = DefaultSlots(LlamaCpp)
	}
	if res.Slots == 1 && res.Capabilities.Window > 0 {
		res.KV, res.KVTokens = Shared, res.Capabilities.Window
	}
	if props.ModelPath != "" {
		res.Details.ModelFile = path.Base(props.ModelPath)
		res.Details.ModelRevision = snapshotOf(props.ModelPath)
	}

	var models struct {
		Data []struct {
			Meta struct {
				TrainContext int   `json:"n_ctx_train"`
				Parameters   int64 `json:"n_params"`
				Size         int64 `json:"size"`
			} `json:"meta"`
		} `json:"data"`
	}
	if p.getJSON(ctx, "/v1/models", &models) && len(models.Data) > 0 {
		meta := models.Data[0].Meta
		res.Details.TrainContext, res.Details.Parameters, res.Details.SizeBytes = meta.TrainContext, meta.Parameters, meta.Size
	}

	res.Capabilities.Counting = p.llamaCppCounting(ctx)
	return res, true
}

// llamaCppCounting checks the two routes exact counting uses: /apply-template
// renders a chat request and /tokenize turns the prompt into tokens.
func (p *prober) llamaCppCounting(ctx context.Context) Counting {
	var rendered struct {
		Prompt string `json:"prompt"`
	}
	chat := p.postJSON(ctx, "/apply-template", map[string]any{"messages": probeMessages}, &rendered) && rendered.Prompt != ""
	text := rendered.Prompt
	if !chat {
		text = "hi"
	}
	var tokens struct {
		Tokens []any `json:"tokens"`
	}
	if !p.postJSON(ctx, "/tokenize", map[string]any{"content": text}, &tokens) || len(tokens.Tokens) == 0 {
		return CountNone
	}
	if chat {
		return CountChat
	}
	return CountText
}

// snapshotOf returns the revision in a Hugging Face cache path, such as
// .../models--org--name/snapshots/<revision>/file.gguf, and "" for a path
// that does not have one.
func snapshotOf(modelPath string) string {
	parts := strings.Split(modelPath, "/")
	for i, part := range parts {
		if part == "snapshots" && i+1 < len(parts)-1 {
			return parts[i+1]
		}
	}
	return ""
}
