package provider

// API is the wire protocol a provider speaks to its endpoint. Both implement
// Provider, so the loop, the accounting and the compaction do not see which is
// in use (docs/design/runtime.md section 1).
type API string

// The protocols an endpoint may be spoken to in.
const (
	// APIChat is Chat Completions, which every OpenAI-compatible server has.
	APIChat API = "chat"
	// APIResponses is the stateless Responses API: store false, the whole
	// input on every call, reasoning items passed back as they were returned.
	APIResponses API = "responses"
)

// Valid reports whether a is a protocol a provider may use.
func (a API) Valid() bool { return a == APIChat || a == APIResponses }
