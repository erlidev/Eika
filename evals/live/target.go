package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
)

// ErrNotConfigured means the bench's endpoint is not set. A live test skips
// on it, saying why, the way a Docker test skips without a daemon.
var ErrNotConfigured = errors.New(EndpointVar + " is not set: put it in ~/.config/eika/live.env or export it (evals/README.md)")

// ErrUnreachable means the endpoint is set but did not answer. A live test
// fails on it rather than skipping, because a skipped live run looks like a
// pass.
var ErrUnreachable = errors.New("live endpoint does not answer")

// probeTimeout bounds each request that reads the server's description of
// itself.
const probeTimeout = 10 * time.Second

// Target is the chat model on the bench: the provider that reaches it, the
// model name a request carries, and what the server said about itself.
type Target struct {
	// Provider is the harness's own OpenAI-compatible provider, pointed at
	// the endpoint.
	Provider provider.Provider
	// Model is the identifier requests name the model by.
	Model string
	// Server describes the build and the model the endpoint serves.
	Server ServerInfo
}

// ServerInfo is what an llama.cpp server reports about itself on /props and
// /v1/models. A field the server does not report is left out.
type ServerInfo struct {
	// Build is the server's build, such as b11942-338fce651.
	Build string `json:"build,omitempty"`
	// Model is the model name requests carry.
	Model string `json:"model"`
	// ModelFile is the name of the model's weights file.
	ModelFile string `json:"model_file,omitempty"`
	// ModelRevision is the revision of the model's repository the file was
	// downloaded from, read from the file's path in the Hugging Face cache.
	ModelRevision string `json:"model_revision,omitempty"`
	// Quantisation is the file type the server reports, such as Q4_K - Medium.
	Quantisation string `json:"quantisation,omitempty"`
	// Parameters is the model's parameter count.
	Parameters int64 `json:"parameters,omitempty"`
	// SizeBytes is the size of the weights in memory.
	SizeBytes int64 `json:"size_bytes,omitempty"`
	// Flags are the server settings /props states.
	Flags Flags `json:"flags"`
}

// Flags are the server settings /props reports. The KV cache type and
// whether the cache is unified are not among them: llama.cpp does not state
// them, so the report cannot, and `command.txt` on the server is where they
// are written.
type Flags struct {
	// Slots is the number of requests the server decodes at once.
	Slots int `json:"slots,omitempty"`
	// Context is the context window, in tokens, shared by all slots.
	Context int `json:"context,omitempty"`
	// TrainContext is the context the model was trained for.
	TrainContext int `json:"train_context,omitempty"`
	// Vision is whether the server reads images.
	Vision bool `json:"vision"`
	// SlotsEndpoint and MetricsEndpoint say whether /slots and /metrics are
	// enabled.
	SlotsEndpoint   bool `json:"slots_endpoint"`
	MetricsEndpoint bool `json:"metrics_endpoint"`
}

// Connect reaches the endpoint in env and describes it. It returns
// ErrNotConfigured when no endpoint is set, and an error wrapping
// ErrUnreachable, with the way to open the SSH forward, when it is set but
// does not answer.
func Connect(ctx context.Context, env Env) (*Target, error) {
	endpoint := strings.TrimSpace(env[EndpointVar])
	if endpoint == "" {
		return nil, ErrNotConfigured
	}
	base, err := baseURL(endpoint)
	if err != nil {
		return nil, err
	}
	key := env[KeyVar]
	client := &http.Client{Timeout: probeTimeout}

	var props propsWire
	if err := getJSON(ctx, client, base+"/props", key, &props); err != nil {
		return nil, fmt.Errorf("read props of %s: %w", base, err)
	}
	var models modelsWire
	if err := getJSON(ctx, client, base+"/v1/models", key, &models); err != nil {
		return nil, fmt.Errorf("read models of %s: %w", base, err)
	}

	info := serverInfo(props, models)
	if info.Model == "" {
		return nil, fmt.Errorf("read models of %s: the server lists no model", base)
	}
	p, err := openai.New(provider.Endpoint{BaseURL: base + "/v1", APIKey: key})
	if err != nil {
		return nil, fmt.Errorf("build provider: %w", err)
	}
	return &Target{Provider: p, Model: info.Model, Server: info}, nil
}

// baseURL returns the server root of an endpoint, which may be given with or
// without the /v1 the provider adds.
func baseURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%s is not an http(s) URL", EndpointVar)
	}
	return strings.TrimSuffix(strings.TrimRight(u.String(), "/"), "/v1"), nil
}

// propsWire is the part of llama.cpp's /props the report records.
type propsWire struct {
	BuildInfo  string          `json:"build_info"`
	ModelAlias string          `json:"model_alias"`
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

// modelsWire is the part of llama.cpp's /v1/models the report records.
type modelsWire struct {
	Data []struct {
		ID   string `json:"id"`
		Meta struct {
			TrainContext int   `json:"n_ctx_train"`
			Parameters   int64 `json:"n_params"`
			Size         int64 `json:"size"`
		} `json:"meta"`
	} `json:"data"`
}

// serverInfo combines the two descriptions into the report's.
func serverInfo(p propsWire, m modelsWire) ServerInfo {
	info := ServerInfo{
		Build:        p.BuildInfo,
		Model:        p.ModelAlias,
		Quantisation: p.ModelFtype,
		Flags: Flags{
			Slots:           p.TotalSlots,
			Context:         p.Defaults.Context,
			Vision:          p.Modalities["vision"],
			SlotsEndpoint:   p.EndpointSlots,
			MetricsEndpoint: p.EndpointMetrics,
		},
	}
	if p.ModelPath != "" {
		info.ModelFile = path.Base(p.ModelPath)
		info.ModelRevision = snapshotOf(p.ModelPath)
	}
	if len(m.Data) > 0 {
		d := m.Data[0]
		info.Model = d.ID
		info.Parameters = d.Meta.Parameters
		info.SizeBytes = d.Meta.Size
		info.Flags.TrainContext = d.Meta.TrainContext
	}
	return info
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

// getJSON reads one JSON document from the server. A transport failure wraps
// ErrUnreachable with the pointer to the README that explains the forward.
func getJSON(ctx context.Context, client *http.Client, target, key string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w; open the SSH forward in evals/models/README.md (make live does when the endpoint is on 127.0.0.1)", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the endpoint refused %s (status %d): check it in ~/.config/eika/live.env", KeyVar, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
