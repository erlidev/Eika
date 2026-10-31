package counter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
)

// Bounds on one tokenizer request: a count is cheap, a rendered prompt of a
// 200k-token session is a few megabytes.
const (
	routeTimeout = 30 * time.Second
	maxResponse  = 64 << 20
)

// Config says where an endpoint's tokenizer routes are and which ones the
// probe found.
type Config struct {
	// BaseURL is the API root the provider uses, such as http://host:8080/v1.
	BaseURL string
	// APIKey is sent as a bearer token when set.
	APIKey string
	// Model is the model a vLLM or SGLang tokenizer request names.
	Model string
	// Backend and Counting are what the probe recorded for the endpoint.
	Backend  probe.Backend
	Counting probe.Counting
}

// Routes is the Source that reaches a server's tokenizer routes over HTTP:
//
//	llama.cpp  POST /apply-template, then POST /tokenize (verified, b11942)
//	vLLM       POST /tokenize, with messages for a chat request (unverified)
//	SGLang     POST /tokenize, text only (unverified)
//	TabbyAPI   POST /v1/token/encode, text only (unverified)
//
// A chat request is rendered from the equivalent Chat Completions body its
// provider builds, so everything that changes the prompt reaches the server.
type Routes struct {
	cfg     Config
	root    string
	encoder provider.ChatEncoder
	client  *http.Client

	mu sync.Mutex
	// markers remembers which candidate strings were found to be control
	// tokens.
	markers map[string]bool
}

// NewRoutes returns the Source for an endpoint. encoder is the provider that
// serialises requests for it.
func NewRoutes(cfg Config, encoder provider.ChatEncoder) *Routes {
	root := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"), "/v1")
	return &Routes{
		cfg: cfg, root: root, encoder: encoder,
		client:  provider.NewHTTPClient(0),
		markers: map[string]bool{},
	}
}

// Level maps what the probe found to what the counter can use. Only llama.cpp
// and vLLM are trusted to render a chat request completely; the others count
// text.
func (r *Routes) Level() Level {
	switch r.cfg.Backend {
	case probe.LlamaCpp, probe.VLLM:
		switch r.cfg.Counting {
		case probe.CountChat:
			return LevelChat
		case probe.CountText:
			return LevelText
		}
	case probe.SGLang, probe.Tabby:
		if r.cfg.Counting != probe.CountNone {
			return LevelText
		}
	}
	return LevelNone
}

// Prompt renders req with llama.cpp's /apply-template.
func (r *Routes) Prompt(ctx context.Context, req provider.Request) (string, error) {
	if r.cfg.Backend != probe.LlamaCpp || r.Level() != LevelChat {
		return "", ErrUnsupported
	}
	body, err := r.chatBody(req)
	if err != nil {
		return "", err
	}
	var out struct {
		Prompt string `json:"prompt"`
	}
	if err := r.post(ctx, "/apply-template", body, &out); err != nil {
		return "", err
	}
	return out.Prompt, nil
}

// Request counts req with vLLM's /tokenize, which renders and tokenises it.
func (r *Routes) Request(ctx context.Context, req provider.Request) (int, error) {
	if r.cfg.Backend != probe.VLLM || r.Level() != LevelChat {
		return 0, ErrUnsupported
	}
	body, err := r.chatBody(req)
	if err != nil {
		return 0, err
	}
	return r.count(ctx, "/tokenize", body)
}

// Text counts text with the backend's text route.
func (r *Routes) Text(ctx context.Context, text string) (int, error) {
	switch {
	case r.Level() == LevelNone:
		return 0, ErrUnsupported
	case r.cfg.Backend == probe.LlamaCpp:
		return r.tokenize(ctx, text, true)
	case r.cfg.Backend == probe.Tabby:
		return r.count(ctx, "/v1/token/encode", mustJSON(map[string]any{"text": text}))
	}
	body := map[string]any{"prompt": text}
	if r.cfg.Model != "" {
		body["model"] = r.cfg.Model
	}
	if r.cfg.Backend == probe.VLLM {
		// The prompt is counted as it is: a BOS token is part of a rendered
		// prompt, not of a piece of text.
		body["add_special_tokens"] = false
	}
	return r.count(ctx, "/tokenize", mustJSON(body))
}

// candidate matches the strings that look like a chat template's control
// tokens: <|im_start|>, <|eot_id|>.
var candidate = regexp.MustCompile(`<\|[^<>|\s]{1,40}\|>`)

// Boundaries returns the starts of the control tokens in a llama.cpp prompt.
// llama.cpp cuts text at its control tokens before tokenising, so a prompt
// cut there counts the same in pieces as whole. A string is taken for a
// control token when it is one token as text and not one with special-token
// parsing off, which a plain vocabulary entry would be, and a candidate is
// asked about once.
func (r *Routes) Boundaries(ctx context.Context, prompt string) ([]int, error) {
	if r.cfg.Backend != probe.LlamaCpp || r.Level() == LevelNone {
		return nil, nil
	}
	var cuts []int
	for _, loc := range candidate.FindAllStringIndex(prompt, -1) {
		ok, err := r.isControl(ctx, prompt[loc[0]:loc[1]])
		if err != nil {
			return nil, err
		}
		if ok {
			cuts = append(cuts, loc[0])
		}
	}
	sort.Ints(cuts)
	return cuts, nil
}

func (r *Routes) isControl(ctx context.Context, s string) (bool, error) {
	r.mu.Lock()
	known, seen := r.markers[s]
	r.mu.Unlock()
	if seen {
		return known, nil
	}
	parsed, err := r.tokenize(ctx, s, true)
	if err != nil {
		return false, err
	}
	literal, err := r.tokenize(ctx, s, false)
	if err != nil {
		return false, err
	}
	control := parsed == 1 && literal != 1
	r.mu.Lock()
	r.markers[s] = control
	r.mu.Unlock()
	return control, nil
}

// tokenize counts text with llama.cpp's /tokenize.
func (r *Routes) tokenize(ctx context.Context, text string, parseSpecial bool) (int, error) {
	return r.count(ctx, "/tokenize", mustJSON(map[string]any{"content": text, "parse_special": parseSpecial}))
}

// chatBody is the provider's chat request for req.
func (r *Routes) chatBody(req provider.Request) ([]byte, error) {
	if r.encoder == nil {
		return nil, ErrUnsupported
	}
	return r.encoder.ChatBody(req)
}

// count posts body and reads the number of tokens the answer states: a count
// field where the server has one, else the length of the token list.
func (r *Routes) count(ctx context.Context, path string, body []byte) (int, error) {
	var out struct {
		Tokens []json.RawMessage `json:"tokens"`
		Count  *int              `json:"count"`
		Length *int              `json:"length"`
	}
	if err := r.post(ctx, path, body, &out); err != nil {
		return 0, err
	}
	switch {
	case out.Count != nil:
		return *out.Count, nil
	case out.Length != nil:
		return *out.Length, nil
	case out.Tokens != nil:
		return len(out.Tokens), nil
	}
	return 0, fmt.Errorf("post %s: the answer has no token count", path)
}

func (r *Routes) post(ctx context.Context, path string, body []byte, into any) error {
	ctx, cancel := context.WithTimeout(ctx, routeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.root+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("post %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(into); err != nil {
		return fmt.Errorf("read %s answer: %w", path, err)
	}
	return nil
}

// mustJSON encodes a map of strings and booleans, which cannot fail.
func mustJSON(v map[string]any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

var _ Source = (*Routes)(nil)
