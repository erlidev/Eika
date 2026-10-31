package live

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
)

// ErrNotConfigured means the bench's endpoint is not set. A live test skips
// on it, saying why, the way a Docker test skips without a daemon.
var ErrNotConfigured = errors.New(EndpointVar + " is not set: put it in ~/.config/eika/live.env or export it (evals/README.md)")

// ErrUnreachable means the endpoint is set but did not answer. A live test
// fails on it rather than skipping, because a skipped live run looks like a
// pass.
var ErrUnreachable = errors.New("live endpoint does not answer")

// Target is the chat model on the bench: the provider that reaches it, the
// model name a request carries, and what the server said about itself.
type Target struct {
	// Provider is the harness's own OpenAI-compatible provider, pointed at
	// the endpoint.
	Provider provider.Provider
	// Model is the identifier requests name the model by.
	Model string
	// Models is what the endpoint lists, as the harness reads it.
	Models []provider.ModelInfo
	// Probe is what the harness's provider probe found out about the
	// endpoint, which is what Server reports.
	Probe probe.Result
	// Server describes the build and the model the endpoint serves.
	Server ServerInfo
	// BaseURL is the provider's API root, with the /v1.
	BaseURL string
	// Key is the bearer key; it never leaves the process.
	Key string
}

// ServerInfo is what the harness's probe reports about the bench's server, as
// the report records it. A field the server does not report is left out.
type ServerInfo struct {
	// Backend is the kind of server the probe identified, such as llamacpp.
	Backend string `json:"backend"`
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
	// Flags are the server settings the probe read.
	Flags Flags `json:"flags"`
}

// Flags are the server settings the probe reports. The KV cache type is not
// among them, and neither is whether the cache is unified: llama.cpp does
// not state either (see KV), so `command.txt` on the server is where they
// are written.
type Flags struct {
	// Slots is the number of requests the server decodes at once.
	Slots int `json:"slots,omitempty"`
	// Context is the context window, in tokens, one request may use, as the
	// server states it.
	Context int `json:"context,omitempty"`
	// TrainContext is the context the model was trained for.
	TrainContext int `json:"train_context,omitempty"`
	// KV is how the probe classed the KV cache: shared, partitioned or
	// unknown, which is what it says for a llama.cpp server with several
	// slots.
	KV string `json:"kv,omitempty"`
	// Vision is whether the server reads images.
	Vision bool `json:"vision"`
	// SlotsEndpoint and MetricsEndpoint say whether /slots and /metrics are
	// enabled.
	SlotsEndpoint   bool `json:"slots_endpoint"`
	MetricsEndpoint bool `json:"metrics_endpoint"`
}

// Connect reaches the endpoint in env and describes it with the harness's
// provider probe. It returns ErrNotConfigured when no endpoint is set, and an
// error wrapping ErrUnreachable, with the way to open the SSH forward, when it
// is set but does not answer.
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
	p, err := openai.New(provider.Endpoint{BaseURL: base + "/v1", APIKey: key})
	if err != nil {
		return nil, fmt.Errorf("build provider: %w", err)
	}
	models, err := p.(provider.Lister).Models(ctx)
	if err != nil {
		return nil, listFailure(base, err)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("read models of %s: the server lists no model", base)
	}
	res := probe.Run(ctx, probe.Target{BaseURL: base + "/v1", APIKey: key, Models: models})
	return &Target{
		Provider: p, Model: models[0].ID, Models: models, Probe: res,
		Server: serverInfo(models[0].ID, res), BaseURL: base + "/v1", Key: key,
	}, nil
}

// listFailure words the failure of the first request to the endpoint: no
// answer at all is ErrUnreachable with the pointer to the README that
// explains the forward, and a refused key names the variable that holds it.
func listFailure(base string, err error) error {
	var perr *provider.Error
	switch {
	case !errors.As(err, &perr) || perr.StatusCode == 0:
		return fmt.Errorf("read models of %s: %w: %w; open the SSH forward in evals/models/README.md (make live does when the endpoint is on 127.0.0.1)", base, ErrUnreachable, err)
	case perr.StatusCode == http.StatusUnauthorized || perr.StatusCode == http.StatusForbidden:
		return fmt.Errorf("read models of %s: the endpoint refused %s (status %d): check it in ~/.config/eika/live.env", base, KeyVar, perr.StatusCode)
	}
	return fmt.Errorf("read models of %s: status %d", base, perr.StatusCode)
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

// serverInfo renders a probe's findings as the report records them.
func serverInfo(model string, r probe.Result) ServerInfo {
	return ServerInfo{
		Backend:       string(r.Backend),
		Build:         r.Capabilities.Build,
		Model:         model,
		ModelFile:     r.Details.ModelFile,
		ModelRevision: r.Details.ModelRevision,
		Quantisation:  r.Details.Quantisation,
		Parameters:    r.Details.Parameters,
		SizeBytes:     r.Details.SizeBytes,
		Flags: Flags{
			Slots:           r.Slots,
			Context:         r.Capabilities.Window,
			TrainContext:    r.Details.TrainContext,
			KV:              string(r.KV),
			Vision:          r.Details.Vision,
			SlotsEndpoint:   r.Details.SlotsEndpoint,
			MetricsEndpoint: r.Details.MetricsEndpoint,
		},
	}
}
