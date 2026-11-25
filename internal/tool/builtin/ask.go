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
// one tool that blocks on a human rather than on the computer.
type askUserTool struct {
	questions *Questions
	relay     func(context.Context, Question) (string, bool, error)
}

// askUserArgs are the parameters of an ask_user call.
type askUserArgs struct {
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	AllowFreeText bool     `json:"allow_free_text"`
}

// Name identifies the tool to the model.
func (askUserTool) Name() string { return "ask_user" }

// Standalone marks ask_user as a tool a chat may offer: it waits on the user,
// not on a computer.
func (askUserTool) Standalone() {}

// ReadOnly marks ask_user as changing nothing: it waits on the user.
func (askUserTool) ReadOnly() bool { return true }

// Description tells the model what the tool does.
func (askUserTool) Description() string {
	return "Ask a question and wait for the answer. In a subagent, Eika receives it and answers or asks the user. " +
		"Use it when a decision is the user's to make and you cannot proceed without it, " +
		"not to report progress. Offer options when the choice is closed."
}

// Schema describes the parameters of an ask_user call.
func (askUserTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "The question, as one sentence, at most 2048 UTF-8 bytes."},
    "options": {
      "type": "array",
      "items": {"type": "string"},
      "description": "At most 10 nonempty answers, each at most 128 UTF-8 bytes. Omit for an open question."
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
	args, err := decodeQuestion(raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	text := strings.TrimSpace(args.Question)
	if text == "" || len(text) > 2048 || strings.ContainsRune(text, 0) {
		return tool.Errorf("ask_user: question must be nonempty and at most 2048 bytes without NUL"), nil
	}
	if len(args.Options) > maxQuestionOptions {
		return tool.Errorf("ask_user: at most %d options are allowed", maxQuestionOptions), nil
	}
	for _, option := range args.Options {
		if strings.TrimSpace(option) == "" || len(option) > 128 || strings.ContainsRune(option, 0) {
			return tool.Errorf("ask_user: options must be nonempty and at most 128 bytes without NUL"), nil
		}
	}
	if t.questions == nil && t.relay == nil {
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
	if t.relay != nil {
		answer, handled, err := t.relay(ctx, asked)
		if handled {
			if err != nil {
				return tool.Result{}, fmt.Errorf("ask user: %w", err)
			}
			return tool.Text(answer), nil
		}
	}
	if t.questions == nil {
		return tool.Errorf("ask_user: nobody is listening for questions in this session"), nil
	}
	pending := t.questions.ask(asked)
	defer t.questions.forget(asked.ID)

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

func decodeQuestion(raw json.RawMessage) (askUserArgs, error) {
	var args askUserArgs
	fields, err := objectArguments(raw, map[string]json.RawMessage{"question": nil, "options": nil, "allow_free_text": nil}, []string{"question"})
	if err != nil {
		return args, err
	}
	args.Question, err = stringArgument("question", fields["question"])
	if err != nil {
		return args, err
	}
	if v, ok := fields["options"]; ok {
		var items []json.RawMessage
		if json.Unmarshal(v, &items) != nil || items == nil {
			return args, fmt.Errorf("options must be a list of strings")
		}
		for _, item := range items {
			text, err := stringArgument("option", item)
			if err != nil {
				return args, err
			}
			args.Options = append(args.Options, text)
		}
	}
	if v, ok := fields["allow_free_text"]; ok {
		var free *bool
		if json.Unmarshal(v, &free) != nil || free == nil {
			return args, fmt.Errorf("allow_free_text must be a boolean")
		}
		args.AllowFreeText = *free
	}
	return args, nil
}

var _ tool.Standalone = askUserTool{}
