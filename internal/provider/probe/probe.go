package probe

import (
	"context"
	"net"
	"net/url"
	"strings"

	"github.com/erlidev/eika/internal/provider"
)

// Backend names the kind of server behind an endpoint.
type Backend string

// The backends a probe tells apart.
const (
	LlamaCpp Backend = "llamacpp"
	VLLM     Backend = "vllm"
	SGLang   Backend = "sglang"
	Tabby    Backend = "tabby"
	// OpenAI is a hosted OpenAI-compatible API that describes nothing about
	// itself beyond its model list.
	OpenAI Backend = "openai"
	// Other is a server on the local network the probe did not recognise.
	Other Backend = "other"
)

// Valid reports whether b is a backend a provider may have.
func (b Backend) Valid() bool {
	switch b {
	case LlamaCpp, VLLM, SGLang, Tabby, OpenAI, Other:
		return true
	}
	return false
}

// Local reports whether the backend runs on the user's own hardware, which is
// what decides how many requests it takes at once by default.
func (b Backend) Local() bool { return b != OpenAI }

// KV says how a pool's KV cache is divided among its requests.
type KV string

// The ways a KV cache is divided.
const (
	// Shared means one cache all requests draw on: a single request may use
	// the whole window, but concurrent requests together cannot exceed the
	// cache.
	Shared KV = "shared"
	// Partitioned means a fixed share per slot.
	Partitioned KV = "partitioned"
	// Unknown means the server does not say and the probe has no reliable
	// way to tell. The inference queue then admits calls as if the cache were
	// shared with the per-request window as its size, which cannot overfill
	// either kind of cache.
	Unknown KV = "unknown"
)

// Valid reports whether k is a value a provider may have.
func (k KV) Valid() bool { return k == Shared || k == Partitioned || k == Unknown }

// Counting says how an endpoint counts the tokens of a request.
type Counting string

// The ways an endpoint counts tokens.
const (
	// CountChat renders a chat request with its template and tools and
	// tokenises it: the exact count of what the model will read.
	CountChat Counting = "chat"
	// CountText tokenises plain text only.
	CountText Counting = "text"
	// CountNone has no tokenizer route.
	CountNone Counting = "none"
)

// CacheMetrics says whether an endpoint reports how much of a prompt it
// served from its cache.
type CacheMetrics string

// The states of an endpoint's cache metrics.
const (
	// CacheReported means the endpoint states the cached part of every prompt.
	CacheReported CacheMetrics = "reported"
	// CacheUnknown means a request is needed to find out, or the endpoint is
	// not known to report it.
	CacheUnknown CacheMetrics = "unknown"
)

// Capabilities is what a probe learned about an endpoint that is not one of
// the provider's own settings. It is stored as the provider's capabilities
// column and sent to the UI as it is, so a later check, such as which API the
// endpoint speaks, adds a field here.
type Capabilities struct {
	// Build is the version the server reported, such as llama.cpp's
	// b11942-338fce651, and is empty when it reported none.
	Build string `json:"build,omitempty"`
	// Window is the context, in tokens, one request may use, as the server
	// states it; zero when the server states none. A hosted API states a
	// window per model, in the model list.
	Window int `json:"window,omitempty"`
	// Counting is how the endpoint counts tokens.
	Counting Counting `json:"counting"`
	// CacheMetrics says whether the endpoint reports cached prompt tokens.
	CacheMetrics CacheMetrics `json:"cache_metrics"`
	// SlotHints is whether a request may name the slot it wants: the server
	// takes the field. Whether naming one helps is SlotAffinity's verdict.
	SlotHints bool `json:"slot_hints"`
	// Responses is the outcome of the Responses conformance check, absent
	// until it has run. Run does not run it: it asks the model, which the
	// routes Run reads never do.
	Responses *Conformance `json:"responses,omitempty"`
	// SlotAffinity is the outcome of the slot affinity check, absent until it
	// has run, and for a server whose requests cannot name a slot. Like
	// Responses it asks the model, so Run does not run it.
	SlotAffinity *SlotAffinity `json:"slot_affinity,omitempty"`
}

// Details is what a server says about the model it loaded. Only llama.cpp
// states it; the live tests record it in their reports.
type Details struct {
	// ModelFile is the name of the weights file.
	ModelFile string
	// ModelRevision is the revision of the model's repository the file was
	// downloaded from, read from the file's path in the Hugging Face cache.
	ModelRevision string
	// Quantisation is the file type the server reports, such as Q4_K - Medium.
	Quantisation string
	// Parameters is the model's parameter count.
	Parameters int64
	// SizeBytes is the size of the weights in memory.
	SizeBytes int64
	// TrainContext is the context the model was trained for.
	TrainContext int
	// Vision is whether the server reads images.
	Vision bool
	// SlotsEndpoint and MetricsEndpoint say whether /slots and /metrics are
	// enabled.
	SlotsEndpoint, MetricsEndpoint bool
}

// Result is the outcome of probing one endpoint.
type Result struct {
	Backend Backend
	// Slots is the concurrent requests the endpoint takes: what the server
	// reports, or the default for its backend when it does not.
	Slots int
	// KV is how the cache is divided, and KVTokens its size in tokens when
	// KV is Shared and the server states it; zero is unknown.
	KV       KV
	KVTokens int
	// Capabilities are the probed facts that are not provider settings.
	Capabilities Capabilities
	// Details describes the loaded model where the server says.
	Details Details
}

// Slot defaults when a server states no number: one request at a time for a
// server on the user's hardware, and eight for a hosted API.
const (
	defaultLocalSlots = 1
	defaultCloudSlots = 8
)

// DefaultSlots is the number of concurrent requests assumed of a backend that
// does not state its own.
func DefaultSlots(b Backend) int {
	if b.Local() {
		return defaultLocalSlots
	}
	return defaultCloudSlots
}

// Target is the endpoint to probe.
type Target struct {
	// BaseURL is the API root the provider uses, such as http://host:8080/v1.
	BaseURL string
	// APIKey is sent as a bearer token when set.
	APIKey string
	// Models is what the endpoint lists, as the provider read it. vLLM and
	// SGLang state the window per model there.
	Models []provider.ModelInfo
}

// Run probes an endpoint. It does not fail: a route that is missing or
// refuses the request means the endpoint is not that backend, and an endpoint
// that nothing identifies is Other, or OpenAI when it is not on the user's
// own network. The caller has already reached the endpoint (it listed the
// models), so a probe that finds nothing is an answer, not an error.
func Run(ctx context.Context, t Target) Result {
	p := newProber(t)
	for _, identify := range []func(context.Context) (Result, bool){
		p.llamaCpp,
		p.sglang,
		p.tabby,
		p.vllm,
	} {
		if res, ok := identify(ctx); ok {
			return res
		}
	}
	return p.generic()
}

// generic describes an endpoint no backend claimed. A hosted API states
// nothing about hardware, and neither does a local server this probe does not
// know, so the two differ only in how many requests they are assumed to take.
func (p *prober) generic() Result {
	b := Other
	if !localHost(p.root) {
		b = OpenAI
	}
	return Result{
		Backend:      b,
		Slots:        DefaultSlots(b),
		KV:           Unknown,
		Capabilities: Capabilities{Counting: CountNone, CacheMetrics: CacheUnknown},
	}
}

// localHost reports whether a URL names a machine on the user's own network:
// a loopback or private address, a name without a dot, or one in a suffix
// that is local by convention. Docker's host.docker.internal is the last.
func localHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".lan", ".internal", ".home.arpa", ".localdomain"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
