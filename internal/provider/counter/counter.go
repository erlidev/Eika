package counter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sync"

	"github.com/erlidev/eika/internal/provider"
)

// Outcome is what a prediction came to once the server measured the request.
type Outcome struct {
	// Prediction is what Predict said.
	Prediction provider.Prediction
	// Measured is the server's prompt count.
	Measured int
	// Drift is the prediction less the measurement. Any drift on an exact
	// prediction is a bug.
	Drift int
}

// Counter predicts the prompt size of the requests of one session and learns
// from their measurements. It is safe for concurrent use; calls are
// serialised, which one session's loop never notices.
type Counter struct {
	src Source

	mu sync.Mutex
	// texts remembers the token count of each text counted, by hash, so a
	// prefix is never tokenised twice.
	texts *memo
	// chain remembers the token count of a prompt up to a cut, by the hash of
	// everything before the cut.
	chain *memo
	// exacts remembers the count of whole requests already asked about.
	exacts *memo
	// splitOff is set when cutting a prompt was found to change its count, and
	// every prompt is then counted whole.
	splitOff bool
	pending  map[[32]byte]*pending
	// order lists the keys of pending, oldest first.
	order  [][32]byte
	model  fit
	ratio  ratio
	anchor *anchor
	// images is the cost in tokens of an image of each size, learned from
	// measurements.
	images map[[2]int]int
	// estimateError is the running mean of the relative error of estimated
	// predictions.
	estimateError float64
}

// New returns a Counter for one session on an endpoint's tokenizer routes.
func New(src Source) *Counter {
	return &Counter{
		src: src, texts: newMemo(), chain: newMemo(), exacts: newMemo(),
		pending: map[[32]byte]*pending{}, images: map[[2]int]int{},
	}
}

// pending is what Predict remembers about a request until Observe sees its
// measurement.
type pending struct {
	pred provider.Prediction
	// base is the count of the request without its images, and method how it
	// was reached.
	base   int
	method provider.Method
	images []provider.Image
	// text, shape and modelled hold the census in tokens when the anchored
	// model was used or could learn from it.
	cen      census
	text     int
	x        vector
	modelled bool
	head     [32]byte
	messages [][32]byte
}

// anchor is the last measured request, which a request that extends it is
// predicted from.
type anchor struct {
	head     [32]byte
	messages [][32]byte
	// measured is the server's count for the request without its images.
	measured int
	bytes    int
	text     int
	x        vector
	modelled bool
}

// maxPending bounds the predictions waiting for a measurement: a loop
// observes each request right after sending it.
const maxPending = 8

// Predict returns the size of req before it is sent, by the best method the
// endpoint offers (docs/design/runtime.md 2). It fails only if ctx ends: a
// route that does not answer lowers the method instead.
func (c *Counter) Predict(ctx context.Context, req provider.Request) (provider.Prediction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, err := c.predict(ctx, req)
	if err != nil {
		return provider.Prediction{}, err
	}
	c.finish(p)
	c.remember(requestKey(req), p)
	return p.pred, nil
}

// Peek returns the size of req by the best method the endpoint offers, as
// Predict does, for a caller that only wants to know: it remembers nothing for
// Observe, so a preview of the next request cannot stand in for the request a
// run sends, and it never fails: when ctx ends first the answer is the
// estimate.
func (c *Counter) Peek(ctx context.Context, req provider.Request) provider.Prediction {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, err := c.predict(ctx, req)
	if err != nil {
		p = c.estimate(req)
	}
	c.finish(p)
	return p.pred
}

// predict sizes req, stripped of its pictures, by the best method available.
func (c *Counter) predict(ctx context.Context, req provider.Request) (*pending, error) {
	stripped, images := stripImages(req)
	p := &pending{images: images}
	p.head, p.messages = keys(stripped)

	if c.src.Level() == LevelChat {
		n, err := c.exact(ctx, stripped)
		if err == nil {
			p.base, p.method = n, provider.MethodExact
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	p.cen = take(stripped)
	if p.method == "" && c.src.Level() >= LevelText && c.model.n > 0 {
		n, err := c.anchored(ctx, p)
		if err == nil {
			p.base, p.method = n, provider.MethodAnchored
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if p.method == "" {
		p.base, p.method = c.estimated(p), provider.MethodEstimated
	}
	return p, nil
}

// Estimate returns the size of req by the estimated method alone, without
// asking the endpoint anything. It is the prediction of last resort for a
// caller whose Predict could not finish in the time it had, and Observe treats
// it like any other prediction of req.
func (c *Counter) Estimate(req provider.Request) provider.Prediction {
	c.mu.Lock()
	defer c.mu.Unlock()

	p := c.estimate(req)
	c.finish(p)
	c.remember(requestKey(req), p)
	return p.pred
}

// estimate sizes req by the estimated method alone.
func (c *Counter) estimate(req provider.Request) *pending {
	stripped, images := stripImages(req)
	p := &pending{images: images}
	p.head, p.messages = keys(stripped)
	p.cen = take(stripped)
	p.base, p.method = c.estimated(p), provider.MethodEstimated
	return p
}

// finish adds the pictures to the count of the text and sets the method the
// whole can claim.
func (c *Counter) finish(p *pending) {
	cost, known := c.imageCost(p.images)
	p.pred = provider.Prediction{Tokens: p.base + cost, Method: p.method}
	if len(p.images) > 0 && p.method == provider.MethodExact {
		// The server counts the text and not the pictures, so the whole is no
		// longer what it will say.
		p.pred.Method = provider.MethodAnchored
	}
	if !known {
		p.pred.Method = provider.MethodEstimated
	}
}

// remember keeps p until its measurement arrives.
func (c *Counter) remember(key [32]byte, p *pending) {
	if _, ok := c.pending[key]; !ok {
		if len(c.order) >= maxPending {
			delete(c.pending, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
	}
	c.pending[key] = p
}

// Observe records what the server measured for req, which Predict was asked
// about, and learns from it. It returns the prediction's outcome; ok is false
// when the usage states no prompt count or Predict never saw the request.
func (c *Counter) Observe(ctx context.Context, req provider.Request, usage provider.Usage) (Outcome, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	measured, stated := usage.InputTokens.Value()
	key := requestKey(req)
	p, seen := c.pending[key]
	if !stated || !seen {
		return Outcome{}, false
	}
	delete(c.pending, key)
	c.order = slices.DeleteFunc(c.order, func(k [32]byte) bool { return k == key })

	out := Outcome{Prediction: p.pred, Measured: measured, Drift: p.pred.Tokens - measured}
	if p.pred.Method == provider.MethodEstimated && measured > 0 {
		c.estimateError = 0.8*c.estimateError + 0.2*math.Abs(float64(out.Drift))/float64(measured)
	}
	c.learn(ctx, p, measured)
	return out, true
}

// Seed teaches a counter that has measured nothing from the measurement of an
// earlier request of the same conversation, such as the one a stored session
// carries after a restart: the learned constants are in memory, and a session
// resumed on an endpoint that counts no better than by bytes would otherwise
// be sized from nothing. It does nothing for a counter that has learned
// already, and it leaves the running error of estimates alone, since nothing
// was predicted. It reports whether it learned.
func (c *Counter) Seed(ctx context.Context, req provider.Request, usage provider.Usage) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	measured, stated := usage.InputTokens.Value()
	if !stated || c.anchor != nil || c.model.n > 0 {
		return false
	}
	stripped, images := stripImages(req)
	p := &pending{images: images, method: provider.MethodEstimated}
	p.head, p.messages = keys(stripped)
	p.cen = take(stripped)
	c.learn(ctx, p, measured)
	return c.anchor != nil
}

// CountText returns the exact token count of text, for a caller that wants a
// number for a text of its own, such as a model's reasoning. It reports false
// when the endpoint has no route that tokenises text or the route fails: the
// caller then has no count, and nothing here estimates one.
func (c *Counter) CountText(ctx context.Context, text string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.src.Level() < LevelText {
		return 0, false
	}
	n, err := c.text(ctx, text)
	return n, err == nil
}

// learn takes the measured prompt of p's request into everything that learns:
// the picture costs, the bytes per token, the anchored model and the anchor.
func (c *Counter) learn(ctx context.Context, p *pending, measured int) {
	cost, known := c.imageCost(p.images)
	if !known {
		c.learnImages(p, measured)
		// The next request that holds these pictures is predicted exactly from
		// the text; this one cannot teach the model anything about its text.
		c.anchor = nil
		return
	}
	text := measured - cost
	c.ratio.add(p.cen.bytes, text)
	if c.src.Level() == LevelText || p.modelled {
		if !p.modelled {
			if t, x, err := c.census(ctx, p.cen); err == nil {
				p.text, p.x, p.modelled = t, x, true
			}
		}
		if p.modelled {
			c.model.add(p.x, float64(text-p.text))
		}
	}
	c.anchor = &anchor{
		head: p.head, messages: p.messages, measured: text,
		bytes: p.cen.bytes, text: p.text, x: p.x, modelled: p.modelled,
	}
}

// EstimateError is the running mean relative error of the estimated
// predictions the counter has made, which the compaction reserve is widened
// by. It is zero until an estimate has been measured.
func (c *Counter) EstimateError() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.estimateError
}

// exact counts a request the endpoint renders itself.
func (c *Counter) exact(ctx context.Context, req provider.Request) (int, error) {
	key := requestKey(req)
	if n, ok := c.exacts.get(key); ok {
		return n, nil
	}
	var n int
	prompt, err := c.src.Prompt(ctx, req)
	switch {
	case err == nil:
		n, err = c.prompt(ctx, prompt)
	case errors.Is(err, ErrUnsupported):
		n, err = c.src.Request(ctx, req)
	}
	if err != nil {
		return 0, err
	}
	c.exacts.put(key, n)
	return n, nil
}

// prompt counts a rendered prompt. It cuts the prompt at the endpoint's
// boundaries and looks for the longest prefix counted before, so a prompt that
// extends the last one tokenises only what was added since.
func (c *Counter) prompt(ctx context.Context, prompt string) (int, error) {
	bounds, err := c.src.Boundaries(ctx, prompt)
	if err != nil {
		return 0, err
	}
	bounds = slices.DeleteFunc(slices.Clone(bounds), func(b int) bool { return b <= 0 || b >= len(prompt) })
	if c.splitOff || len(bounds) == 0 {
		return c.text(ctx, prompt)
	}
	type cut struct {
		at   int
		hash [32]byte
	}
	cuts := make([]cut, len(bounds))
	var hash [32]byte
	from := 0
	for i, b := range bounds {
		piece := sha256.Sum256([]byte(prompt[from:b]))
		hash = sha256.Sum256(append(hash[:], piece[:]...))
		cuts[i] = cut{at: b, hash: hash}
		from = b
	}

	count, from := 0, 0
	for i := len(cuts) - 1; i >= 0; i-- {
		if n, ok := c.chain.get(cuts[i].hash); ok {
			count, from = n, cuts[i].at
			break
		}
	}
	last := cuts[len(cuts)-1]
	cold := from == 0
	if from < last.at {
		n, err := c.text(ctx, prompt[from:last.at])
		if err != nil {
			return 0, err
		}
		count += n
		c.chain.put(last.hash, count)
	}
	tail, err := c.text(ctx, prompt[last.at:])
	if err != nil {
		return 0, err
	}
	total := count + tail
	if cold {
		// Nothing was reused, so counting the prompt whole costs nothing the
		// pieces did not: it checks that cutting did not change the count.
		whole, err := c.text(ctx, prompt)
		if err != nil {
			return 0, err
		}
		if whole != total {
			c.splitOff = true
			return whole, nil
		}
	}
	return total, nil
}

// text counts one text, once.
func (c *Counter) text(ctx context.Context, text string) (int, error) {
	key := sha256.Sum256([]byte(text))
	if n, ok := c.texts.get(key); ok {
		return n, nil
	}
	n, err := c.src.Text(ctx, text)
	if err != nil {
		return 0, err
	}
	c.texts.put(key, n)
	return n, nil
}

// census tokenises the texts of a census and returns their total and the
// terms of the model.
func (c *Counter) census(ctx context.Context, cen census) (int, vector, error) {
	total := 0
	for _, s := range cen.texts {
		n, err := c.text(ctx, s)
		if err != nil {
			return 0, vector{}, err
		}
		total += n
	}
	x := cen.shape
	for _, r := range []struct {
		texts []string
		term  int
	}{{cen.live, termLive}, {cen.old, termOld}} {
		for _, s := range r.texts {
			n, err := c.text(ctx, s)
			if err != nil {
				return 0, vector{}, err
			}
			x[r.term] += float64(n)
		}
	}
	return total, x, nil
}

// anchored predicts from the exact count of the text and the learned share of
// the template, starting from the last measurement when the request extends
// it.
func (c *Counter) anchored(ctx context.Context, p *pending) (int, error) {
	text, x, err := c.census(ctx, p.cen)
	if err != nil {
		return 0, err
	}
	p.text, p.x, p.modelled = text, x, true
	w := c.model.weights()
	if a := c.anchor; a != nil && a.modelled && a.extendedBy(p.head, p.messages) {
		return round(float64(a.measured) + float64(text-a.text) + x.minus(a.x).dot(w)), nil
	}
	return round(float64(text) + x.dot(w)), nil
}

// estimated predicts from bytes and the learned bytes per token, starting
// from the last measurement when the request extends it.
func (c *Counter) estimated(p *pending) int {
	per := c.ratio.bytesPerToken()
	if a := c.anchor; a != nil && a.extendedBy(p.head, p.messages) {
		return round(float64(a.measured) + float64(p.cen.bytes-a.bytes)/per)
	}
	return round(float64(p.cen.bytes) / per)
}

// extendedBy reports whether a request with these keys is the anchored
// request followed by more messages (or the same request again).
func (a *anchor) extendedBy(head [32]byte, messages [][32]byte) bool {
	return a.head == head && len(a.messages) <= len(messages) &&
		slices.Equal(a.messages, messages[:len(a.messages)])
}

// EstimatedImageTokens is the cost taken for a picture of w by h pixels whose
// size has not been measured, near what vision models charge for the sizes
// Eika sends: one token per 750 pixels. A size that is not known is taken to
// be as large as the harness sends.
func EstimatedImageTokens(w, h int) int {
	const pixelsPerToken = 750
	if w <= 0 || h <= 0 {
		w, h = 1920, 1080
	}
	return (w*h + pixelsPerToken - 1) / pixelsPerToken
}

// imageCost totals the cost of images and says whether every one was measured.
func (c *Counter) imageCost(images []provider.Image) (int, bool) {
	total, known := 0, true
	for _, img := range images {
		if n, ok := c.images[[2]int{img.Width, img.Height}]; ok {
			total += n
			continue
		}
		known = false
		total += EstimatedImageTokens(img.Width, img.Height)
	}
	return total, known
}

// learnImages learns what a picture costs from a request whose text was
// counted exactly: the measurement less the text and the pictures already
// known is the cost of the one size not yet known. Several unknown sizes in
// one request cannot be told apart and teach nothing.
func (c *Counter) learnImages(p *pending, measured int) {
	if p.method != provider.MethodExact {
		return
	}
	rest := measured - p.base
	var unknown [2]int
	n := 0
	for _, img := range p.images {
		dims := [2]int{img.Width, img.Height}
		if cost, ok := c.images[dims]; ok {
			rest -= cost
			continue
		}
		if n > 0 && dims != unknown {
			return
		}
		unknown = dims
		n++
	}
	if n > 0 && rest > 0 && rest%n == 0 {
		c.images[unknown] = rest / n
	}
}

// stripImages returns req with the pictures taken out of its messages, and
// the pictures. The endpoint's routes do not count a picture, so what they are
// asked about is the request without them.
func stripImages(req provider.Request) (provider.Request, []provider.Image) {
	var images []provider.Image
	for _, m := range req.Messages {
		images = append(images, m.Images...)
	}
	if len(images) == 0 {
		return req, nil
	}
	req.Messages = slices.Clone(req.Messages)
	for i := range req.Messages {
		req.Messages[i].Images = nil
	}
	return req, images
}

// requestKey identifies a request by everything in it that can change the
// prompt or the count, pictures included. The most the model may generate is
// not among them: a template does not render it, and the agent lowers it to
// the room a window leaves after it has predicted the prompt, so the request
// that is observed differs from the one that was predicted in that field alone.
func requestKey(req provider.Request) [32]byte {
	messages := slices.Clone(req.Messages)
	for i := range messages {
		messages[i].Metrics = nil
	}
	req.Messages = messages
	req.Sampling.MaxOutput = nil
	raw, _ := json.Marshal(req)
	return sha256.Sum256(raw)
}

// keys returns the hash of a request without its messages and the hash of
// each message, which say whether one request extends another.
func keys(req provider.Request) (head [32]byte, messages [][32]byte) {
	messages = make([][32]byte, len(req.Messages))
	for i, m := range req.Messages {
		m.Metrics = nil
		raw, _ := json.Marshal(m)
		messages[i] = sha256.Sum256(raw)
	}
	req.Messages = nil
	return requestKey(req), messages
}
