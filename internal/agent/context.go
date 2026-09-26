package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/erlidev/eika/internal/provider"
)

// bytesPerToken is how many bytes EstimateTokens counts as one token. It is
// the usual rule of thumb for English prose and code; a tokenizer is not
// worth carrying for a figure the provider measures after the call.
const bytesPerToken = 4

// EstimateTokens estimates how many tokens text costs, at four bytes to a
// token. Every size the agent reports before a model has measured it comes
// from here, so all of them are estimates of one kind.
func EstimateTokens(text string) int {
	return (len(text) + bytesPerToken - 1) / bytesPerToken
}

// SectionKind names the part of the system prompt a Section is.
type SectionKind string

// The parts of a system prompt, in the order they appear.
const (
	// SectionBase is the base prompt: WorkspacePrompt, ChatPrompt, or the
	// Options.BasePrompt that replaces them.
	SectionBase SectionKind = "base"
	// SectionContextFiles is the workspace's context files, rendered as one
	// block under a heading that says how they combine.
	SectionContextFiles SectionKind = "context_files"
	// SectionInstructions is Options.Instructions.
	SectionInstructions SectionKind = "instructions"
)

// Section is one named part of the system prompt. A system prompt is its
// sections' texts joined by a blank line.
type Section struct {
	Kind SectionKind `json:"kind"`
	// Text is exactly what the section adds to the system prompt.
	Text string `json:"text"`
	// Tokens is the estimated size of Text.
	Tokens int `json:"tokens"`
	// Files are the files a SectionContextFiles section renders, from the
	// most general to the most specific.
	Files []ContextFile `json:"files,omitempty"`
}

// ContextFile is one context file as its section renders it.
type ContextFile struct {
	// Path is the file's workspace path.
	Path string `json:"path"`
	// Text is the file's content as the model reads it.
	Text string `json:"text"`
	// Tokens is the estimated size of Text.
	Tokens int `json:"tokens"`
}

// ToolSource says where a tool the model is offered comes from.
type ToolSource string

// The places a tool can come from.
const (
	// ToolBuiltin is a tool the harness itself implements.
	ToolBuiltin ToolSource = "builtin"
	// ToolMCP is a tool an MCP server offers.
	ToolMCP ToolSource = "mcp"
)

// mcpToolPrefix begins the name of every tool that reaches an MCP server:
// mcp__<server>__<tool> for a server's own tools, and mcp_list_resources and
// mcp_read_resource for the ones that read servers' resources. No built-in
// tool name starts with it.
const mcpToolPrefix = "mcp_"

// sourceOf reports where the tool named name comes from.
func sourceOf(name string) ToolSource {
	if strings.HasPrefix(name, mcpToolPrefix) {
		return ToolMCP
	}
	return ToolBuiltin
}

// ToolSchema is one tool definition as a request sends it.
type ToolSchema struct {
	provider.ToolDef
	Source ToolSource `json:"source"`
	// Tokens is the estimated size of the definition's JSON.
	Tokens int `json:"tokens"`
}

// Parameters are what a request sends beside its content.
type Parameters struct {
	// Model is the model's identifier on its endpoint.
	Model string `json:"model"`
	// Sampling is every sampling parameter the request sends; a nil field
	// is left to the endpoint.
	Sampling provider.Sampling `json:"sampling"`
	// ThinkingSwitch is the field that carries the effort "none".
	ThinkingSwitch provider.ThinkingSwitch `json:"thinking_switch,omitempty"`
	// PreserveThinking says whether earlier reasoning is replayed.
	PreserveThinking bool `json:"preserve_thinking"`
}

// Context is one model request as the agent assembles it: the system prompt
// by section, the tool schemas, the messages exactly as sent, and the
// parameters. A run's model call and Preview build it with the same code,
// and Request turns it into what the provider receives.
type Context struct {
	Sections []Section    `json:"sections"`
	Tools    []ToolSchema `json:"tools"`
	// Messages are the conversation as the provider receives it: no
	// metrics, and reasoning only when PreserveThinking replays it.
	Messages []provider.Message `json:"messages"`
	// MessageTokens is the estimated size of Messages.
	MessageTokens int `json:"message_tokens"`
	// MessageSizes is the estimated size of each of Messages, in order.
	MessageSizes []int      `json:"message_sizes"`
	Parameters   Parameters `json:"parameters"`
}

// System returns the system prompt the sections make.
func (c Context) System() string {
	texts := make([]string, len(c.Sections))
	for i, s := range c.Sections {
		texts[i] = s.Text
	}
	return strings.Join(texts, "\n\n")
}

// Request returns the provider request c describes.
func (c Context) Request() provider.Request {
	var tools []provider.ToolDef
	for _, t := range c.Tools {
		tools = append(tools, t.ToolDef)
	}
	return provider.Request{
		Model:            c.Parameters.Model,
		System:           c.System(),
		Messages:         slices.Clone(c.Messages),
		Tools:            tools,
		Sampling:         c.Parameters.Sampling,
		ThinkingSwitch:   c.Parameters.ThinkingSwitch,
		PreserveThinking: c.Parameters.PreserveThinking,
	}
}

// Preview returns the request the next model call on s would send, with the
// sections it is built from. It reads the context files and assembles the
// request as a run does, so a run started now sends exactly this, with the
// run's new message, if it has one, appended to Messages. Nothing is sent,
// and the context window bound is not applied.
func (a *Agent) Preview(ctx context.Context, s *Session) (Context, error) {
	if s == nil || s.Conversation == nil {
		return Context{}, errors.New("preview request: session has no conversation")
	}
	prompt, err := a.prompt(ctx)
	if err != nil {
		return Context{}, fmt.Errorf("preview request: %w", err)
	}
	return a.assemble(s, prompt), nil
}

// WithMessages returns c with msgs as its messages, prepared as a request
// sends them, and MessageTokens and MessageSizes estimated for them. A
// recorded model call keeps no messages of its own; it is rebuilt with the
// session's path down to the entry its conversation ended at.
func (c Context) WithMessages(msgs []provider.Message) Context {
	c.Messages = make([]provider.Message, 0, len(msgs))
	c.MessageSizes = make([]int, 0, len(msgs))
	c.MessageTokens = 0
	for _, m := range msgs {
		// Metrics belong to session replay, and reasoning goes back to the
		// model only when it is preserved: no provider receives either
		// otherwise, so neither is part of the request.
		m.Metrics = nil
		if !c.Parameters.PreserveThinking {
			m.Reasoning = ""
		}
		size := messageTokens(m)
		c.Messages = append(c.Messages, m)
		c.MessageSizes = append(c.MessageSizes, size)
		c.MessageTokens += size
	}
	return c
}

// assemble builds the request for the next model call on s from the system
// prompt sections of the run.
func (a *Agent) assemble(s *Session, prompt []Section) Context {
	c := Context{
		Sections: append([]Section{}, prompt...),
		Tools:    []ToolSchema{},
		Parameters: Parameters{
			Model:            a.opts.Model,
			Sampling:         a.opts.Sampling,
			ThinkingSwitch:   a.opts.ThinkingSwitch,
			PreserveThinking: a.opts.PreserveThinking,
		},
	}
	if a.tools != nil {
		for _, def := range a.tools.Schemas() {
			c.Tools = append(c.Tools, ToolSchema{ToolDef: def, Source: sourceOf(def.Name), Tokens: ToolTokens(def)})
		}
	}
	return c.WithMessages(s.Conversation.Messages())
}

// ToolTokens estimates the size of one tool definition as JSON, as a
// request sends it. A schema that is not valid JSON cannot be encoded; its
// text is estimated instead.
func ToolTokens(def provider.ToolDef) int {
	data, err := json.Marshal(def)
	if err != nil {
		return EstimateTokens(def.Name + def.Description + string(def.Schema))
	}
	return EstimateTokens(string(data))
}

// messageTokens estimates the size of one message as JSON.
func messageTokens(m provider.Message) int {
	data, err := json.Marshal(m)
	if err != nil {
		return EstimateTokens(m.Content + m.Reasoning)
	}
	return EstimateTokens(string(data))
}

// Recorder keeps a record of the model calls a run makes, so a client can
// see exactly what each one sent. Record is called once for every model call
// that returned a response, however many attempts that took, and before the
// next call. By then every message the call sent is in the Store and its
// reply is not, so the session's newest stored message, the head of its
// session tree, is the one the call's conversation ended at. A call that
// fails is not recorded: the messages only it sent are taken back out of the
// conversation, so nothing stored marks where it ended.
type Recorder interface {
	Record(ctx context.Context, c ModelCall) error
}

// ModelCall is one finished model call, as a Recorder receives it.
type ModelCall struct {
	// RunID is the turn that made the call: the run_id of its events and of
	// its reply's metrics.
	RunID string
	// SessionID is the session the call was made for.
	SessionID string
	// Context is the request the call sent and the sections it was built
	// from.
	Context Context
	// Usage is what the endpoint measured for this call alone, and zero when
	// it reported nothing.
	Usage provider.Usage
}

// record tells the Recorder about one finished model call. A record is for
// inspection and the conversation it describes is already stored, so a
// failure to keep one is logged and the run goes on. It outlives a run
// cancelled after the response arrived, as the stored messages do.
func (a *Agent) record(ctx context.Context, s *Session, runID string, c Context, usage provider.Usage) {
	if a.opts.Recorder == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
	defer cancel()
	call := ModelCall{RunID: runID, SessionID: s.ID, Context: c, Usage: usage}
	if err := a.opts.Recorder.Record(recordCtx, call); err != nil {
		a.opts.Logger.Error("record model call", "session_id", s.ID, "run_id", runID, "error", err)
	}
}
