package session

import (
	"encoding/json"
	"fmt"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// MessageEntry returns the entry that records one provider message, tagged
// with the workspace HEAD commit the message was produced at. Empty commits
// are not recorded.
//
// One message is one entry, including an assistant message that carries tool
// calls: the payload is the message's own JSON, so an entry round-trips to
// exactly the message it came from. Splitting the tool calls into entries of
// their own would be a second representation of the same turn, and the tree
// would gain branch points the agent loop cannot resume from, because a model
// that asked for three calls needs all three answered.
func MessageEntry(m provider.Message, commit string) (store.Entry, error) {
	kind, err := kindOf(m.Role)
	if err != nil {
		return store.Entry{}, err
	}
	payload, err := json.Marshal(withArguments(m))
	if err != nil {
		return store.Entry{}, fmt.Errorf("encode %s message: %w", m.Role, err)
	}
	return store.Entry{Kind: kind, Payload: payload, Commit: commit}, nil
}

// withArguments returns m with every tool call's arguments spelled as a JSON
// document. A model that calls a tool without parameters may send nothing at
// all, and nothing is not JSON: it would make the whole payload invalid and
// fail the append.
func withArguments(m provider.Message) provider.Message {
	empty := false
	for _, c := range m.ToolCalls {
		empty = empty || len(c.Arguments) == 0
	}
	if !empty {
		return m
	}
	calls := make([]provider.ToolCall, len(m.ToolCalls))
	copy(calls, m.ToolCalls)
	for i := range calls {
		if len(calls[i].Arguments) == 0 {
			calls[i].Arguments = json.RawMessage(`{}`)
		}
	}
	m.ToolCalls = calls
	return m
}

// Message returns the provider message an entry records. The second result is
// false for an entry that is not part of the conversation: system notes and
// events are shown in the user interface and are not sent to the model.
func Message(e store.Entry) (provider.Message, bool, error) {
	switch e.Kind {
	case store.KindUser, store.KindAssistant, store.KindToolCall, store.KindToolResult:
	default:
		return provider.Message{}, false, nil
	}
	var m provider.Message
	if err := json.Unmarshal(e.Payload, &m); err != nil {
		return provider.Message{}, false, fmt.Errorf("decode %s entry %s: %w", e.Kind, e.ID, err)
	}
	return m, true, nil
}

// Messages returns the conversation a path of entries stands for.
func Messages(entries []store.Entry) ([]provider.Message, error) {
	out := make([]provider.Message, 0, len(entries))
	for _, e := range entries {
		m, ok, err := Message(e)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// kindOf maps a message role onto the entry kind that records it.
func kindOf(role provider.Role) (store.EntryKind, error) {
	switch role {
	case provider.RoleUser:
		return store.KindUser, nil
	case provider.RoleAssistant:
		return store.KindAssistant, nil
	case provider.RoleTool:
		return store.KindToolResult, nil
	default:
		return "", fmt.Errorf("record message: unknown role %q", role)
	}
}
