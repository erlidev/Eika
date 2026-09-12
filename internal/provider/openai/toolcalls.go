package openai

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/erlidev/eika/internal/provider"
)

// toolCalls assembles tool calls that arrive as fragments spread over many
// chunks. The stream identifies a call by its index; the id, the name, and the
// argument fragments each arrive when the model gets to them.
type toolCalls struct {
	byIndex map[int64]*partialCall
}

// partialCall is one tool call being assembled.
type partialCall struct {
	id   string
	name string
	args strings.Builder
}

// newToolCalls returns an empty accumulator.
func newToolCalls() *toolCalls {
	return &toolCalls{byIndex: map[int64]*partialCall{}}
}

// add folds one streamed fragment into the call it belongs to.
func (t *toolCalls) add(delta openai.ChatCompletionChunkChoiceDeltaToolCall) {
	call, ok := t.byIndex[delta.Index]
	if !ok {
		call = &partialCall{}
		t.byIndex[delta.Index] = call
	}
	if delta.ID != "" {
		call.id = delta.ID
	}
	if delta.Function.Name != "" {
		call.name = delta.Function.Name
	}
	call.args.WriteString(delta.Function.Arguments)
}

// calls returns the assembled calls ordered by their stream index. Arguments
// that are empty become an empty JSON object, which is what a tool with no
// required parameters expects.
func (t *toolCalls) calls() []provider.ToolCall {
	indexes := make([]int64, 0, len(t.byIndex))
	for i := range t.byIndex {
		indexes = append(indexes, i)
	}
	sort.Slice(indexes, func(a, b int) bool { return indexes[a] < indexes[b] })

	out := make([]provider.ToolCall, 0, len(indexes))
	for _, i := range indexes {
		call := t.byIndex[i]
		args := strings.TrimSpace(call.args.String())
		if args == "" {
			args = "{}"
		}
		out = append(out, provider.ToolCall{ID: call.id, Name: call.name, Arguments: json.RawMessage(args)})
	}
	return out
}
