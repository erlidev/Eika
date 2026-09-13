package event

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event type names. Every event streamed by Eika carries exactly one of these
// in its Type field.
const (
	// TypeTurnStart reports that an assistant turn has begun.
	TypeTurnStart = "turn.start"
	// TypeMessageDelta carries an incremental piece of assistant output.
	TypeMessageDelta = "message.delta"
	// TypeMessageReset tells clients to discard deltas from a failed model
	// attempt before a retry starts.
	TypeMessageReset = "message.reset"
	// TypeToolCall reports that the assistant asked for a tool call.
	TypeToolCall = "tool.call"
	// TypeToolOutput carries incremental output from a running tool.
	TypeToolOutput = "tool.output"
	// TypeToolResult reports the final result of a tool call.
	TypeToolResult = "tool.result"
	// TypeTurnEnd reports that an assistant turn finished.
	TypeTurnEnd = "turn.end"
	// TypeRunError reports that an agent run failed.
	TypeRunError = "run.error"
	// TypeQuestionAsked reports that the agent is waiting on a user answer.
	TypeQuestionAsked = "question.asked"
	// TypeSubagentStarted reports that a child agent run has begun.
	TypeSubagentStarted = "subagent.started"
	// TypeSubagentFinished reports that a child agent run has ended.
	TypeSubagentFinished = "subagent.finished"
	// TypeWorkspaceState reports a workspace lifecycle transition.
	TypeWorkspaceState = "workspace.state"
	// TypeSessionMessage carries one stored session entry, which is how a
	// client replays the messages it missed.
	TypeSessionMessage = "session.message"
	// TypeBusDropped reports that the client fell behind and lost events.
	TypeBusDropped = "bus.dropped"
)

// TopicGlobal is the topic for events that belong to no single workspace or
// session. Workspace and session topics are "workspace:<id>" and
// "session:<id>".
const TopicGlobal = "global"

// Event is the envelope for everything Eika streams to a client. Payload is
// the JSON encoding of a struct owned by the package that emits the event; its
// shape is determined by Type.
type Event struct {
	Type    string          `json:"type"`
	Topic   string          `json:"topic"`
	Time    time.Time       `json:"time"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// New builds an Event of the given type and topic, stamped with the current
// UTC time, encoding payload as JSON. A nil payload yields an event with no
// payload field.
func New(typ, topic string, payload any) (Event, error) {
	e := Event{Type: typ, Topic: topic, Time: time.Now().UTC()}
	if payload == nil {
		return e, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s payload: %w", typ, err)
	}
	e.Payload = data
	return e, nil
}

// WorkspaceTopic returns the topic carrying events for one workspace.
func WorkspaceTopic(id string) string { return "workspace:" + id }

// SessionTopic returns the topic carrying events for one session.
func SessionTopic(id string) string { return "session:" + id }

// DecodePayload decodes an event's payload into v.
func (e Event) DecodePayload(v any) error {
	if len(e.Payload) == 0 {
		return fmt.Errorf("decode %s payload: event has no payload", e.Type)
	}
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("decode %s payload: %w", e.Type, err)
	}
	return nil
}
