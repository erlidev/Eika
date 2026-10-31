package counter

import (
	"context"
	"errors"

	"github.com/erlidev/eika/internal/provider"
)

// ErrUnsupported is what a Source returns from a method its endpoint has no
// route for.
var ErrUnsupported = errors.New("the endpoint has no route for this")

// Level is how much of the counting an endpoint does for the runtime.
type Level int

// The levels, from least to most.
const (
	// LevelNone means the endpoint has no tokenizer route: sizes are
	// estimated.
	LevelNone Level = iota
	// LevelText means it tokenises plain text, so a request's text is counted
	// exactly and the template's share is learned.
	LevelText
	// LevelChat means it renders a chat request with its template and
	// tools, so the whole request is counted exactly.
	LevelChat
)

// Source is the tokenizer routes of one endpoint, as the counter uses them. A
// method the endpoint lacks returns ErrUnsupported. Routes is the HTTP
// implementation; providertest ships a fake with a deterministic tokenizer.
type Source interface {
	// Level says what the endpoint can count.
	Level() Level
	// Prompt returns the text the endpoint's chat template makes of req: what
	// the model is given, before tokenising. Only llama.cpp shows it.
	Prompt(ctx context.Context, req provider.Request) (string, error)
	// Request returns the exact token count of req, rendered and tokenised by
	// the endpoint in one route, for an endpoint that does not show the
	// prompt (vLLM).
	Request(ctx context.Context, req provider.Request) (int, error)
	// Text returns the token count of text on its own.
	Text(ctx context.Context, text string) (int, error)
	// Boundaries returns the positions in a prompt at which it can be cut
	// into pieces whose token counts add up to the count of the whole: the
	// starts of control tokens, which a tokenizer never merges across. It is
	// empty when the endpoint cannot say.
	Boundaries(ctx context.Context, prompt string) ([]int, error)
}
