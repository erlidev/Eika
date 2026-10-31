package counter

import (
	"encoding/json"
	"math"

	"github.com/erlidev/eika/internal/provider"
)

// The terms of the anchored model. A server's prompt count for a request is
// taken to be the exact count of its text plus a weighted sum of these, whose
// weights are learned from measurements:
//
//	base         what every request carries: the start of the prompt, the
//	             generation prompt, the wrapper around the tool list
//	user, assistant, tool
//	             what the template adds around one message of that role
//	call         what it adds around one tool call
//	live, old    the tokens of replayed reasoning in assistant messages after
//	             the last user message and before it. A template that keeps all
//	             of it learns 1 and 1, one that drops what came before the
//	             last question learns 1 and 0.
const (
	termBase = iota
	termUser
	termAssistant
	termTool
	termCall
	termLive
	termOld
	terms
)

// vector holds one number per term.
type vector [terms]float64

func (v vector) minus(w vector) vector {
	for i := range v {
		v[i] -= w[i]
	}
	return v
}

func (v vector) dot(w vector) float64 {
	sum := 0.0
	for i := range v {
		sum += v[i] * w[i]
	}
	return sum
}

// census is what a request is made of, in the terms the model uses.
type census struct {
	// texts are counted as they are, with weight 1: the system prompt, the
	// tool definitions, message content and tool calls.
	texts []string
	// live and old are the replayed reasoning texts, weighed by what was
	// learned.
	live, old []string
	// shape holds the counts of the structural terms; the reasoning terms are
	// filled in from the token counts of live and old.
	shape vector
	// bytes is the size of everything above, for the estimate.
	bytes int
}

// take reads a request, with its images already taken out, into a census.
func take(req provider.Request) census {
	var c census
	add := func(s string) {
		if s != "" {
			c.texts = append(c.texts, s)
			c.bytes += len(s)
		}
	}
	add(req.System)
	for _, def := range req.Tools {
		if raw, err := json.Marshal(def); err == nil {
			add(string(raw))
		}
	}
	lastUser := -1
	for i, m := range req.Messages {
		if m.Role == provider.RoleUser {
			lastUser = i
		}
	}
	c.shape[termBase] = 1
	for i, m := range req.Messages {
		switch m.Role {
		case provider.RoleUser:
			c.shape[termUser]++
		case provider.RoleAssistant:
			c.shape[termAssistant]++
		case provider.RoleTool:
			c.shape[termTool]++
		}
		add(m.Content)
		for _, call := range m.ToolCalls {
			c.shape[termCall]++
			add(call.Name)
			add(string(call.Arguments))
		}
		if req.PreserveThinking && m.Role == provider.RoleAssistant && m.Reasoning != "" {
			c.bytes += len(m.Reasoning)
			if i > lastUser {
				c.live = append(c.live, m.Reasoning)
			} else {
				c.old = append(c.old, m.Reasoning)
			}
		}
	}
	return c
}

// prior is what a term is taken to weigh before measurements say otherwise,
// and ridge how strongly: each measured request pulls the weight from the prior
// toward what the data says, and a term that has not varied yet keeps its
// prior. The base has none, so a large constant (a tool list's boilerplate)
// is not spread over the roles; the reasoning terms have a weak one because
// their values are hundreds of tokens and settle on the first reasoning seen.
// The role priors are a ChatML-sized wrapper: a start token, a role word, a
// newline, an end token and a newline.
var (
	prior = vector{termBase: 0, termUser: 5, termAssistant: 5, termTool: 5, termCall: 6, termLive: 1, termOld: 1}
	ridge = vector{termBase: 1e-6, termUser: 0.02, termAssistant: 0.02, termTool: 0.02, termCall: 0.02, termLive: 1e-3, termOld: 1e-3}
)

// fit accumulates the normal equations of every measurement and solves them
// for the weights. A request is one row: its terms against what the server
// counted beyond the text.
type fit struct {
	a [terms][terms]float64
	b vector
	n int
}

func (f *fit) add(x vector, y float64) {
	for i := range x {
		for j := range x {
			f.a[i][j] += x[i] * x[j]
		}
		f.b[i] += x[i] * y
	}
	f.n++
}

// weights solves the least squares problem by Gaussian elimination with
// partial pivoting, pulling each weight toward its prior.
func (f *fit) weights() vector {
	var m [terms][terms + 1]float64
	for i := range m {
		copy(m[i][:terms], f.a[i][:])
		m[i][i] += ridge[i]
		m[i][terms] = f.b[i] + ridge[i]*prior[i]
	}
	for col := range terms {
		pivot := col
		for r := col + 1; r < terms; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[pivot][col]) {
				pivot = r
			}
		}
		m[col], m[pivot] = m[pivot], m[col]
		for r := col + 1; r < terms; r++ {
			factor := m[r][col] / m[col][col]
			for k := col; k <= terms; k++ {
				m[r][k] -= factor * m[col][k]
			}
		}
	}
	var w vector
	for i := terms - 1; i >= 0; i-- {
		sum := m[i][terms]
		for k := i + 1; k < terms; k++ {
			sum -= m[i][k] * w[k]
		}
		w[i] = sum / m[i][i]
	}
	return w
}

// defaultBytesPerToken is the ratio used until a measurement says otherwise.
const defaultBytesPerToken = 4

// ratio learns how many bytes a token is from every measured request.
type ratio struct {
	bytes, tokens float64
}

func (r *ratio) add(bytes, tokens int) {
	r.bytes += float64(bytes)
	r.tokens += float64(tokens)
}

func (r ratio) bytesPerToken() float64 {
	if r.tokens == 0 || r.bytes == 0 {
		return defaultBytesPerToken
	}
	return r.bytes / r.tokens
}

// round turns a model's value into a prediction, which is never negative.
func round(v float64) int {
	if v < 0 {
		return 0
	}
	return int(math.Round(v))
}
