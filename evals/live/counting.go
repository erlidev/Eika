package live

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
)

// countingMaxOutput bounds the replies of the counting scenarios, which are
// about the prompt and not the answer.
const countingMaxOutput = 32

// requireExact fails a call whose prediction was not exact: on the bench's
// llama.cpp every request without a picture is counted by the server itself.
func requireExact(c Call) error {
	if c.Method != string(provider.MethodExact) {
		return fmt.Errorf("%s: the prediction was %q, want exact: the server renders and tokenises this request", c.Label, c.Method)
	}
	return nil
}

// quiet is the sampling of the counting scenarios: no thinking, a short
// reply.
func quiet() provider.Sampling {
	maxOutput, temperature, effort := countingMaxOutput, 0.0, provider.EffortNone
	return provider.Sampling{MaxOutput: &maxOutput, Temperature: &temperature, ReasoningEffort: &effort}
}

// ToolsAndReasoning is the scenario that counts a request with tool
// definitions, a tool call and its result, replayed reasoning, and a request
// whose thinking switch differs, and requires every prediction to be exact
// with no drift. It makes three model calls. The history before the first is
// written here, the way the agent loop would hold it after two tool rounds.
func ToolsAndReasoning(nonce string) Scenario {
	return Scenario{
		Name: "tools_and_reasoning",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			req := toolsAndReasoningRequest(nonce)

			first, reply, err := t.Complete(ctx, "after two tool rounds", req)
			calls := []Call{first}
			if err != nil {
				return calls, err
			}
			if err := requireExact(first); err != nil {
				return calls, err
			}

			req.Messages = append(req.Messages,
				provider.AssistantMessageWithReasoning(reply, "The notes are two lines; summarise them.", nil),
				provider.UserMessage("And which of those is most urgent?"))
			second, reply, err := t.Complete(ctx, "after a follow-up", req)
			calls = append(calls, second)
			if err != nil {
				return calls, err
			}
			if err := requireExact(second); err != nil {
				return calls, err
			}

			// Without the switch the template renders a different preamble, so
			// this fails if the counted request leaves anything out.
			req.Messages = append(req.Messages,
				provider.AssistantMessage(reply, nil),
				provider.UserMessage("Thanks. Anything else on the list?"))
			req.ThinkingSwitch, req.Sampling.ReasoningEffort = "", nil
			third, _, err := t.Complete(ctx, "thinking left on", req)
			calls = append(calls, third)
			if err != nil {
				return calls, err
			}
			return calls, requireExact(third)
		},
	}
}

// ImageInput is the scenario that checks what the server's counting routes
// do with a picture. They render and tokenise text; the bench's llama.cpp
// leaves a placeholder where the picture goes and does not count the tokens
// the picture becomes. So a request with a picture must not be called exact:
// the first of a given size is an estimate, and the second, once the cost of
// that size was measured, is counted to the token. It makes two calls, and does
// nothing against a server that does not read images.
func ImageInput() Scenario {
	return Scenario{
		Name: "image_input",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			if !t.Server.Flags.Vision {
				return nil, nil
			}
			picture, err := gradient(56, 56)
			if err != nil {
				return nil, err
			}
			ask := func(text string) provider.Message {
				m := provider.UserMessage(text)
				m.Images = []provider.Image{picture}
				return m
			}
			req := provider.Request{
				ThinkingSwitch: provider.SwitchTemplate,
				Sampling:       quiet(),
				Messages:       []provider.Message{ask("What is in this picture?")},
			}
			first, reply, err := t.Complete(ctx, "first picture", req)
			calls := []Call{first}
			if err != nil {
				return calls, err
			}
			if first.Method == string(provider.MethodExact) {
				return calls, fmt.Errorf("first picture: a request with a picture was called exact, but the server's routes do not count the picture")
			}

			req.Messages = append(req.Messages, provider.AssistantMessage(reply, nil), ask("And this one?"))
			second, _, err := t.Complete(ctx, "second picture of the same size", req)
			calls = append(calls, second)
			if err != nil {
				return calls, err
			}
			if second.Method == string(provider.MethodExact) {
				return calls, fmt.Errorf("second picture: a request with a picture was called exact")
			}
			if second.Drift == nil || *second.Drift != 0 {
				return calls, fmt.Errorf("second picture: drift %s, want 0 once the cost of a picture of this size was measured", second.drift())
			}
			return calls, nil
		},
	}
}

// textOnly is a Source that tokenises text and renders nothing: the bench's
// server seen the way an endpoint with only a text route is.
type textOnly struct{ counter.Source }

func (textOnly) Level() counter.Level { return counter.LevelText }

func (textOnly) Prompt(context.Context, provider.Request) (string, error) {
	return "", counter.ErrUnsupported
}

func (textOnly) Request(context.Context, provider.Request) (int, error) {
	return 0, counter.ErrUnsupported
}

func (textOnly) Boundaries(context.Context, string) ([]int, error) { return nil, nil }

// anchoredWarmUp is the number of calls the anchored model may use to learn the
// template before its drift is held to anchoredBound.
const anchoredWarmUp = 4

// anchoredBound is the most tokens an anchored prediction may be off by once
// the template's share is learned. The bench's tokenizer is not additive over
// the pieces of a message (a word split across two parts counts differently),
// so exactness is not promised, and the bound is the one the scenario found.
const anchoredBound = 4

// AnchoredCounting is the scenario that uses the bench's server as an endpoint
// with a text route only: the counter tokenises the text and learns the
// template's share from the measurements. It plays eight calls with tool
// rounds and replayed reasoning in between, and holds the drift after the
// first few to a stated bound.
func AnchoredCounting(nonce string) Scenario {
	return Scenario{
		Name: "anchored_counting",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			t.Counter = counter.New(textOnly{t.Source})
			req := provider.Request{
				System:           fmt.Sprintf("Session %s.\nYou are Eika. Answer in one short sentence.", nonce),
				ThinkingSwitch:   provider.SwitchTemplate,
				PreserveThinking: true,
				Sampling:         quiet(),
				Tools: []provider.ToolDef{
					{Name: "read_file", Description: "Read a text file.", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
				},
			}
			questions := []string{
				"What is a cache?", "Why do queues matter?", "What does a scheduler do?", "What is a lease?",
				"Name a prefix.", "What is a token?", "Why bound output?", "What is drift?",
			}
			var calls []Call
			for i, q := range questions {
				if i%3 == 2 {
					id := fmt.Sprintf("call_%d", i)
					req.Messages = append(req.Messages,
						provider.AssistantMessageWithReasoning("", fmt.Sprintf("Round %d: the user wants a file read before the answer.", i),
							[]provider.ToolCall{{ID: id, Name: "read_file", Arguments: provider.ToolArguments(fmt.Sprintf(`{"path":"/home/eika/note%d.md"}`, i))}}),
						provider.ToolResultMessage(id, fmt.Sprintf("Note %d: remember the milk.", i), false))
				}
				req.Messages = append(req.Messages, provider.UserMessage(q))
				call, reply, err := t.Complete(ctx, fmt.Sprintf("call %d", i+1), req)
				calls = append(calls, call)
				if err != nil {
					return calls, err
				}
				if call.Method == string(provider.MethodExact) {
					return calls, fmt.Errorf("%s: a text-only endpoint was predicted exact", call.Label)
				}
				if i >= anchoredWarmUp && call.Drift != nil && (*call.Drift > anchoredBound || *call.Drift < -anchoredBound) {
					return calls, fmt.Errorf("%s: anchored drift %d, want within %d once the template is learned", call.Label, *call.Drift, anchoredBound)
				}
				if reply == "" {
					// A reply cut off inside its thinking leaves no text, which
					// a server refuses to be sent back as an assistant message.
					reply = "I am not sure."
				}
				req.Messages = append(req.Messages, provider.AssistantMessageWithReasoning(reply, "Thinking about "+q, nil))
			}
			return calls, nil
		},
	}
}

// gradient returns a w by h PNG picture.
func gradient(w, h int) (provider.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return provider.Image{}, fmt.Errorf("encode picture: %w", err)
	}
	return provider.Image{MediaType: "image/png", Data: buf.Bytes(), Width: w, Height: h}, nil
}

// countCostTokens is the context the count_cost scenario builds: a session of
// a realistic size.
const countCostTokens = 20000

// CountCost is the scenario that measures what counting a request costs at a
// realistic size. It builds a conversation of about 20k tokens, counts it cold
// (the whole prompt is rendered and tokenised), sends it, adds one exchange and
// counts again (only the new messages are tokenised), and sends that. Each call
// reports its count time in count_seconds.
func CountCost(nonce string) Scenario {
	return Scenario{
		Name: "count_cost",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			rng := rand.New(rand.NewSource(1))
			req := provider.Request{
				System:         fmt.Sprintf("Session %s.\nYou are Eika. Answer in one word.", nonce),
				ThinkingSwitch: provider.SwitchTemplate,
				Sampling:       quiet(),
			}
			// About 2000 tokens a message, so 5 exchanges of 4k: enough
			// messages for the prefix to be cut at many places.
			for tokens := 0; tokens < countCostTokens; tokens += 2000 {
				req.Messages = append(req.Messages, provider.UserMessage(prose(rng, 1500)))
				if tokens+2000 < countCostTokens {
					req.Messages = append(req.Messages, provider.AssistantMessage(prose(rng, 300), nil))
				}
			}
			cold, reply, err := t.Complete(ctx, "cold, about 20k tokens", req)
			calls := []Call{cold}
			if err != nil {
				return calls, err
			}
			if err := requireExact(cold); err != nil {
				return calls, err
			}
			req.Messages = append(req.Messages, provider.AssistantMessage(reply, nil), provider.UserMessage(prose(rng, 120)))
			warm, _, err := t.Complete(ctx, "one exchange later", req)
			calls = append(calls, warm)
			if err != nil {
				return calls, err
			}
			return calls, requireExact(warm)
		},
	}
}

// vocabulary is the words prose draws from. A few hundred distinct words keep
// a long text from being a repeated phrase, which a tokenizer would count
// unlike real text.
var vocabulary = strings.Fields(`the a of to and in is that it for with as on be at by this from or an are
file directory cache queue token window prompt model session memory budget
schedule agent tool result error report summary notes plan project task
read write list search open close start stop retry wait send receive parse
because however therefore although while before after during between without
quickly slowly carefully often rarely always never sometimes usually later
home work garden kitchen office server network database service request
Monday Tuesday Wednesday morning evening week month year hour minute second
one two three four five six seven eight nine ten hundred thousand`)

// prose returns about n words of deterministic text from a random source.
func prose(rng *rand.Rand, n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			if i%17 == 0 {
				b.WriteString(".\n")
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteString(vocabulary[rng.Intn(len(vocabulary))])
	}
	b.WriteByte('.')
	return b.String()
}

func toolsAndReasoningRequest(nonce string) provider.Request {
	schema := func(props string) []byte {
		return []byte(`{"type":"object","properties":{` + props + `},"required":["path"]}`)
	}
	return provider.Request{
		System:           fmt.Sprintf("Session %s.\nYou are Eika, a personal assistant. Use the tools to look at files, then answer briefly.", nonce),
		ThinkingSwitch:   provider.SwitchTemplate,
		PreserveThinking: true,
		Sampling:         quiet(),
		Tools: []provider.ToolDef{
			{Name: "list_dir", Description: "List the entries of a directory.", Schema: schema(`"path":{"type":"string","description":"Directory to list."}`)},
			{Name: "read_file", Description: "Read a text file and return its content.", Schema: schema(`"path":{"type":"string","description":"File to read."}`)},
		},
		Messages: []provider.Message{
			provider.UserMessage("What is in /home/eika, and what do my notes say?"),
			provider.AssistantMessageWithReasoning("I will start with the directory.",
				"The user wants the directory first, then the notes. List /home/eika to find the notes file.",
				[]provider.ToolCall{{ID: "call_1", Name: "list_dir", Arguments: `{"path":"/home/eika"}`}}),
			provider.ToolResultMessage("call_1", "notes.md\ntodo.txt\nprojects/", false),
			provider.AssistantMessageWithReasoning("",
				"There is a notes.md. Read it.",
				[]provider.ToolCall{{ID: "call_2", Name: "read_file", Arguments: `{"path":"/home/eika/notes.md"}`}}),
			provider.ToolResultMessage("call_2", "Call the bank on Monday.\nBuy milk.", false),
		},
	}
}
