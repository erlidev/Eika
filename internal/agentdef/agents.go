package agentdef

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed prompts/*.md
var prompts embed.FS

// Compaction holds the built-in instructions for summary calls.
type Compaction struct {
	Summary    string `json:"summary"`
	Update     string `json:"update"`
	TurnPrefix string `json:"turn_prefix"`
}

// Definition is one built-in agent with its fixed capabilities.
type Definition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Class       string     `json:"class"`
	Prompt      string     `json:"prompt"`
	Tools       []string   `json:"tools"`
	MaxTurns    int        `json:"max_turns"`
	Compaction  Compaction `json:"compaction"`
}

// Override contains the fields the user can replace. Nil uses the built-in.
type Override struct {
	Class  *string `json:"class"`
	Prompt *string `json:"prompt"`
}

// Validate rejects unsupported classes, NUL bytes and prompts larger than 64 KiB.
func (o Override) Validate() error {
	if o.Class != nil && *o.Class != "main" && *o.Class != "fast" && *o.Class != "deep" {
		return fmt.Errorf("class must be main, fast or deep")
	}
	if o.Prompt != nil && len(*o.Prompt) > 64<<10 {
		return fmt.Errorf("prompt must be at most 65536 bytes")
	}
	if o.Prompt != nil && strings.ContainsRune(*o.Prompt, '\x00') {
		return fmt.Errorf("prompt must not contain NUL")
	}
	return nil
}

// Apply returns d with the user's class and prompt where set.
func (o Override) Apply(d Definition) Definition {
	if o.Class != nil {
		d.Class = *o.Class
	}
	if o.Prompt != nil {
		d.Prompt = *o.Prompt
	}
	return d
}

// Builtins returns all definitions in stable name order.
func Builtins() []Definition {
	return []Definition{
		definition("eika", "Converses, plans and delegates work.", "main", 40, []string{"project_create", "project_list", "project_open", "project_update",
			"subagent_cancel", "subagent_message", "subagent_start", "subagent_status", "web_search"}),
		definition("researcher", "Reads the web and reports findings.", "fast", 200, []string{"web_fetch", "web_search"}),
		definition("worker", "Does work on a computer and reports results.", "main", 200, []string{"bash", "file_edit", "file_read", "file_write", "web_fetch", "web_search"}),
	}
}

// Lookup returns the named built-in, or false for an unknown name.
func Lookup(name string) (Definition, bool) {
	for _, d := range Builtins() {
		if d.Name == name {
			return d, true
		}
	}
	return Definition{}, false
}

func definition(name, description, class string, turns int, tools []string) Definition {
	read := func(suffix string) string {
		// All paths are compile-time assets covered by the definition tests.
		b, _ := prompts.ReadFile("prompts/" + name + suffix + ".md")
		return string(b)
	}
	return Definition{Name: name, Description: description, Class: class, MaxTurns: turns, Tools: tools,
		Prompt: read(""), Compaction: Compaction{Summary: read("-summary"), Update: read("-update"), TurnPrefix: read("-turn-prefix")}}
}
