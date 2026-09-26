package utility

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/provider"
)

// TitlePrompt is the system prompt of the session title task.
const TitlePrompt = "Write a short title, at most six words, for a conversation that begins with " +
	"the user's message. Do not answer the message. Respond with only the session title."

// Bounds on the session title task.
const (
	// MaxTitleSource is how many bytes of the first message are sent: the
	// start of a long paste says what it is about.
	MaxTitleSource = 4000
	// MaxTitle is the most characters a title keeps.
	MaxTitle = 80
	// titleMaxOutput leaves a model whose thinking cannot be turned off room
	// to think and still answer; with thinking off, a title stops far sooner.
	titleMaxOutput = 1024
)

// ErrNoTitle reports a reply that held no usable title.
var ErrNoTitle = errors.New("the model replied with no title")

// thinkBlock is reasoning an endpoint left in the answer's text, which it
// does when it does not parse the model's reasoning apart.
var thinkBlock = regexp.MustCompile(`(?s)^\s*<think>.*?</think>`)

// Title asks m for the title of a session whose first message is message.
func Title(ctx context.Context, m Model, message string) (string, error) {
	reply, _, err := provider.Complete(ctx, m.Provider, m.request(TitlePrompt, truncate(message, MaxTitleSource), titleMaxOutput))
	if err != nil {
		return "", err
	}
	title := cleanTitle(reply)
	if title == "" {
		return "", ErrNoTitle
	}
	return title, nil
}

// cleanTitle reduces a reply to the title in it: its first line of text,
// without the quotes, markup, label, and final full stop models add.
func cleanTitle(reply string) string {
	reply = thinkBlock.ReplaceAllString(reply, "")
	var line string
	for l := range strings.Lines(reply) {
		if line = strings.TrimSpace(l); line != "" {
			break
		}
	}
	line = strings.TrimLeft(line, "#*_ ")
	if label, rest, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(label), "title") {
		line = rest
	}
	line = strings.Trim(line, " \t*_`\"'“”‘’")
	line = strings.TrimSuffix(line, ".")
	line = strings.Join(strings.Fields(line), " ")
	if utf8.RuneCountInString(line) > MaxTitle {
		line = strings.TrimSpace(string([]rune(line)[:MaxTitle]))
	}
	return line
}

// truncate cuts s to at most n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
