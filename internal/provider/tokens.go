package provider

// Method says how a token count was reached, and so how far to trust it.
type Method string

// The ways a request's size is predicted before it is sent.
const (
	// MethodExact means the endpoint rendered and tokenised the request itself.
	// A prediction of this method that differs from the measured prompt is a
	// bug.
	MethodExact Method = "exact"
	// MethodAnchored means the text of the request was tokenised exactly and
	// the part the template adds was taken from earlier measurements.
	MethodAnchored Method = "anchored"
	// MethodEstimated means part of the count is a ratio learned from earlier
	// measurements, or a stated default: shown with a tilde, and the
	// compaction reserve is widened by its observed error.
	MethodEstimated Method = "estimated"
)

// Prediction is the size of a request the runtime is about to send.
type Prediction struct {
	// Tokens is the predicted prompt size, template included and cached
	// tokens included, the way a server's prompt count is.
	Tokens int `json:"tokens"`
	// Method says how it was reached.
	Method Method `json:"method"`
}

// ChatEncoder is implemented by a Provider that can state the Chat
// Completions request equivalent to a Request: the JSON body its endpoint
// would be sent. The token counter posts that body to a server's own
// rendering route, so what is counted is what the provider sends, from the
// one serialiser. A provider of another protocol builds the equivalent chat
// body too; that is how exact counting works under either.
type ChatEncoder interface {
	ChatBody(req Request) ([]byte, error)
}
