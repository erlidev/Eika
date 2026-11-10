package providertest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
)

// Routes says which tokenizer routes a TokenServer serves.
type Routes int

// The routes a fake endpoint can serve, from most to least.
const (
	// RoutesPrompt renders a chat request and tokenises text, as llama.cpp
	// does: /apply-template and /tokenize.
	RoutesPrompt Routes = iota
	// RoutesRequest counts a whole chat request in one route and does not
	// show the prompt, as vLLM does.
	RoutesRequest
	// RoutesText tokenises plain text only.
	RoutesText
	// RoutesNone has no tokenizer route.
	RoutesNone
)

// TokenServer is a fake model server with a deterministic tokenizer and chat
// template, so a test can know exactly how many tokens a request costs. It is
// a provider.Provider whose reported prompt count is the real count of the
// request it received, and a counter.Source for the routes it serves.
//
// The tokenizer cuts text at control tokens such as <|end|>, which are one
// token each, and counts every run of non-space characters between them as
// one token per three characters, rounded up. The template wraps each message
// in <|start|>role ... <|end|>, ends with the generation prompt, replays the
// reasoning of assistant messages after the last user message when the
// request asks to preserve it, and ignores images in the text; an image costs
// its own tokens, which only the reported prompt count includes.
type TokenServer struct {
	routes Routes

	mu          sync.Mutex
	steps       []Step
	next        int
	textCalls   int
	textBytes   int
	promptCalls int
	requestCall int
}

// NewTokenServer returns a fake endpoint serving the routes, that answers with
// the steps in order. Each answer reports the prompt count of its request,
// and the tokens of the text it streams as its output.
func NewTokenServer(routes Routes, steps ...Step) *TokenServer {
	return &TokenServer{routes: routes, steps: steps}
}

var controlToken = regexp.MustCompile(`<\|[a-z]+\|>`)

// Tokens is the fake tokenizer's count for text.
func Tokens(text string) int {
	n, from := 0, 0
	words := func(s string) {
		for _, w := range strings.Fields(s) {
			n += (len(w) + 2) / 3
		}
	}
	for _, loc := range controlToken.FindAllStringIndex(text, -1) {
		words(text[from:loc[0]])
		n++
		from = loc[1]
	}
	words(text[from:])
	return n
}

// ImageTokens is what the fake server charges for a picture of w by h pixels,
// which no route reports.
func ImageTokens(w, h int) int { return w*h/100 + 3 }

// Render is the fake template applied to req.
func (s *TokenServer) Render(req provider.Request) string {
	var b strings.Builder
	turn := func(role, body string) {
		fmt.Fprintf(&b, "<|start|>%s\n%s<|end|>\n", role, body)
	}
	if req.System != "" {
		turn("system", req.System)
	}
	if len(req.Tools) > 0 {
		var defs []string
		for _, def := range req.Tools {
			raw, _ := json.Marshal(def)
			defs = append(defs, string(raw))
		}
		turn("tools", strings.Join(defs, "\n"))
	}
	lastUser := -1
	for i, m := range req.Messages {
		if m.Role == provider.RoleUser {
			lastUser = i
		}
	}
	for i, m := range req.Messages {
		switch m.Role {
		case provider.RoleUser:
			turn("user", m.ModelText())
		case provider.RoleTool:
			turn("function_result", m.Content)
		case provider.RoleAssistant:
			body := ""
			if req.PreserveThinking && m.Reasoning != "" && i > lastUser {
				body += m.Reasoning + "\n"
			}
			body += m.Content
			for _, c := range m.ToolCalls {
				body += fmt.Sprintf("\n<|call|>%s %s<|endcall|>", c.Name, c.Arguments)
			}
			turn("assistant", body)
		}
	}
	b.WriteString("<|start|>assistant\n")
	return b.String()
}

// PromptTokens is the number of tokens the fake server reads in req: the
// rendered prompt and every image.
func (s *TokenServer) PromptTokens(req provider.Request) int {
	n := Tokens(s.Render(req))
	for _, m := range req.Messages {
		for _, img := range m.Images {
			n += ImageTokens(img.Width, img.Height)
		}
	}
	return n
}

// Level reports what the routes can count.
func (s *TokenServer) Level() counter.Level {
	switch s.routes {
	case RoutesPrompt, RoutesRequest:
		return counter.LevelChat
	case RoutesText:
		return counter.LevelText
	}
	return counter.LevelNone
}

// Prompt is /apply-template.
func (s *TokenServer) Prompt(_ context.Context, req provider.Request) (string, error) {
	if s.routes != RoutesPrompt {
		return "", counter.ErrUnsupported
	}
	s.mu.Lock()
	s.promptCalls++
	s.mu.Unlock()
	return s.Render(req), nil
}

// Request is vLLM's /tokenize with messages.
func (s *TokenServer) Request(_ context.Context, req provider.Request) (int, error) {
	if s.routes != RoutesRequest {
		return 0, counter.ErrUnsupported
	}
	s.mu.Lock()
	s.requestCall++
	s.mu.Unlock()
	return Tokens(s.Render(req)), nil
}

// Text is /tokenize.
func (s *TokenServer) Text(_ context.Context, text string) (int, error) {
	if s.routes == RoutesNone {
		return 0, counter.ErrUnsupported
	}
	s.mu.Lock()
	s.textCalls++
	s.textBytes += len(text)
	s.mu.Unlock()
	return Tokens(text), nil
}

// Boundaries cuts a prompt at every control token.
func (s *TokenServer) Boundaries(_ context.Context, prompt string) ([]int, error) {
	if s.routes != RoutesPrompt {
		return nil, nil
	}
	var cuts []int
	for _, loc := range controlToken.FindAllStringIndex(prompt, -1) {
		cuts = append(cuts, loc[0])
	}
	return cuts, nil
}

// TextCalls is how many texts were tokenised, and TextBytes their total size.
func (s *TokenServer) TextCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textCalls
}

// TextBytes is the total size of the texts tokenised so far.
func (s *TokenServer) TextBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.textBytes
}

// RenderCalls is how many prompts or whole requests the server was asked to
// render for counting.
func (s *TokenServer) RenderCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.promptCalls + s.requestCall
}

// Stream answers with the next scripted step and reports the real prompt
// count of the request in its usage.
func (s *TokenServer) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	s.mu.Lock()
	if s.next >= len(s.steps) {
		s.mu.Unlock()
		return nil, fmt.Errorf("providertest: no scripted response for call %d", s.next+1)
	}
	step := s.steps[s.next]
	s.next++
	s.mu.Unlock()
	if step.Err != nil {
		return nil, step.Err
	}

	prompt := s.PromptTokens(req)
	out := 0
	for _, e := range step.Events {
		out += Tokens(e.Text)
	}
	events := append([]provider.Event(nil), step.Events...)
	usage := provider.Event{Kind: provider.KindUsage, Usage: Usage(prompt, out, prompt+out)}
	events = append(events[:len(events)-1], usage, events[len(events)-1])

	ch := make(chan provider.Event)
	go func() {
		defer close(ch)
		for _, e := range events {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

var (
	_ provider.Provider = (*TokenServer)(nil)
	_ counter.Source    = (*TokenServer)(nil)
)
