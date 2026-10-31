package counter

import (
	"context"
	"encoding/json"
	"math"

	"github.com/erlidev/eika/internal/provider"
)

// maxRenderedTools bounds how many tools are sized by rendering the request
// with each prefix of the list: that is one request to the endpoint per tool,
// and a very long list is sized as text instead.
const maxRenderedTools = 64

// Size counts text as one part of a request. It is exact where the endpoint
// tokenises text, and otherwise (or when ctx ends first) estimated from the
// bytes per token learned so far; exact says which. It never fails and never
// scales anything to fit a total: the caller states what the parts leave
// unexplained.
func (c *Counter) Size(ctx context.Context, text string) (tokens int, exact bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size(ctx, text)
}

// size is Size with the lock held.
func (c *Counter) size(ctx context.Context, text string) (int, bool) {
	if text == "" {
		return 0, true
	}
	if c.src.Level() >= LevelText && ctx.Err() == nil {
		if n, err := c.text(ctx, text); err == nil {
			return n, true
		}
	}
	return int(math.Ceil(float64(len(text)) / c.ratio.bytesPerToken())), false
}

// ImageSize is what a picture costs the endpoint: the cost learned from a
// measurement of a picture of its size, or the pixel estimate until one has
// been measured. measured says which.
func (c *Counter) ImageSize(img provider.Image) (tokens int, measured bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imageCost([]provider.Image{img})
}

// ToolSizes counts each tool definition of req, in order, and reports whether
// the counts are the endpoint's own.
//
// Where the endpoint renders a request (the exact method) each tool is the
// difference the template makes when the tool is added to the tools before it,
// so the sizes add up to what the whole list costs, including the wrapper the
// template puts around the list, which the first tool carries. Counts of
// requests are remembered, and a tool list rarely changes. Otherwise a tool is
// its definition as JSON text, counted as text where the endpoint can and
// estimated where it cannot, and exact is false: the template renders a tool
// differently from its JSON.
func (c *Counter) ToolSizes(ctx context.Context, req provider.Request) (sizes []int, exact bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(req.Tools) == 0 {
		return nil, true
	}
	if c.src.Level() == LevelChat && len(req.Tools) <= maxRenderedTools {
		if sizes, err := c.renderedToolSizes(ctx, req); err == nil {
			return sizes, true
		}
	}
	sizes = make([]int, len(req.Tools))
	for i, def := range req.Tools {
		raw, err := json.Marshal(def)
		if err != nil {
			raw = []byte(def.Name + def.Description + string(def.Schema))
		}
		sizes[i], _ = c.size(ctx, string(raw))
	}
	return sizes, false
}

// renderedToolSizes sizes the tools of req by rendering a request with no
// messages of the conversation's, only one the template needs to render at
// all, with none of the tools and then with each prefix of them.
func (c *Counter) renderedToolSizes(ctx context.Context, req provider.Request) ([]int, error) {
	probe := provider.Request{
		Model:            req.Model,
		Messages:         []provider.Message{provider.UserMessage("hi")},
		ThinkingSwitch:   req.ThinkingSwitch,
		PreserveThinking: req.PreserveThinking,
	}
	before, err := c.exact(ctx, probe)
	if err != nil {
		return nil, err
	}
	sizes := make([]int, len(req.Tools))
	for i := range req.Tools {
		probe.Tools = req.Tools[:i+1]
		with, err := c.exact(ctx, probe)
		if err != nil {
			return nil, err
		}
		sizes[i], before = with-before, with
	}
	return sizes, nil
}
