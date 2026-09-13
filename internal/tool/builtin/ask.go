package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/tool"
)

// maxQuestionOptions bounds how many choices one question offers, so that a
// model cannot fill the user interface with a list nobody can read.
const maxQuestionOptions = 10

// askUserTool asks the user a question and waits for the answer. It is the
// one tool that blocks on a human rather than on the workspace.
type askUserTool struct {
	questions *Questions
}

// askUserArgs are the parameters of an ask_user call.
type askUserArgs struct {
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	AllowFreeText bool     `json:"allow_free_text"`
}

// Name identifies the tool to the model.
func (askUserTool) Name() string { return "ask_user" }

// Description tells the model what the tool does.
func (askUserTool) Description() string {
	return "Ask the user a question and wait for the answer. " +
		"Use it when a decision is the user's to make and you cannot proceed without it, " +
		"not to report progress. Offer options when the choice is closed."
}

// Schema describes the parameters of an ask_user call.
func (askUserTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "The question, as one sentence."},
    "options": {
      "type": "array",
      "items": {"type": "string"},
      "description": "The answers to choose from. Omit for an open question."
    },
    "allow_free_text": {
      "type": "boolean",
      "description": "Accept an answer outside the options. Defaults to false when options are given."
    }
  },
  "required": ["question"],
  "additionalProperties": false
}`)
}

// Call registers the question, reports it as a question.asked event, and
// blocks until the answer arrives or the run is cancelled.
func (t askUserTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[askUserArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	text := strings.TrimSpace(args.Question)
	if text == "" {
		return tool.Errorf("ask_user: question is required"), nil
	}
	if len(args.Options) > maxQuestionOptions {
		return tool.Errorf("ask_user: at most %d options are allowed", maxQuestionOptions), nil
	}
	if t.questions == nil {
		return tool.Errorf("ask_user: nobody is listening for questions in this session"), nil
	}

	asked := Question{
		ID:            newQuestionID(),
		SessionID:     c.SessionID,
		RunID:         c.RunID,
		CallID:        c.CallID,
		Text:          text,
		Options:       args.Options,
		AllowFreeText: args.AllowFreeText || len(args.Options) == 0,
		AskedAt:       time.Now().UTC(),
	}
	pending := t.questions.ask(asked)
	defer t.questions.forget(asked.ID)

	e, err := event.New(event.TypeQuestionAsked, event.SessionTopic(c.SessionID), event.QuestionAsked{
		RunID:         asked.RunID,
		SessionID:     asked.SessionID,
		CallID:        asked.CallID,
		QuestionID:    asked.ID,
		Question:      asked.Text,
		Options:       asked.Options,
		AllowFreeText: asked.AllowFreeText,
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("ask user: %w", err)
	}
	if c.Emit != nil {
		c.Emit.Emit(ctx, e)
	}

	select {
	case answer := <-pending.answer:
		return tool.Text(answer), nil
	case <-ctx.Done():
		return tool.Result{}, fmt.Errorf("ask user: %w", ctx.Err())
	}
}

var _ tool.Tool = askUserTool{}
